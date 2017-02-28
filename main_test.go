package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/gitaly"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/upstream"

	pb "gitlab.com/gitlab-org/gitaly/protos/go"
	"google.golang.org/grpc"
)

const scratchDir = "testdata/scratch"
const testRepoRoot = "testdata/data"
const testDocumentRoot = "testdata/public"
const testRepo = "group/test.git"
const testProject = "group/test"

var checkoutDir = path.Join(scratchDir, "test")
var cacheDir = path.Join(scratchDir, "cache")
var gitalySocketPath = path.Join(scratchDir, "gitaly.sock")

func TestMain(m *testing.M) {
	source := "https://gitlab.com/gitlab-org/gitlab-test.git"
	clonePath := path.Join(testRepoRoot, testRepo)
	if _, err := os.Stat(clonePath); err != nil {
		testCmd := exec.Command("git", "clone", "--bare", source, clonePath)
		testCmd.Stdout = os.Stdout
		testCmd.Stderr = os.Stderr

		if err := testCmd.Run(); err != nil {
			log.Printf("Test setup: failed to run %v", testCmd)
			os.Exit(-1)
		}
	}

	cleanup, err := testhelper.BuildExecutables()
	if err != nil {
		log.Printf("Test setup: failed to build executables: %v", err)
		os.Exit(1)
	}

	os.Exit(func() int {
		defer cleanup()
		return m.Run()
	}())
}

func TestAllowedClone(t *testing.T) {
	// Prepare clone directory
	if err := os.RemoveAll(scratchDir); err != nil {
		t.Fatal(err)
	}

	// Prepare test server and backend
	ts := testAuthServer(nil, 200, gitOkBody(t))
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Do the git clone
	cloneCmd := exec.Command("git", "clone", fmt.Sprintf("%s/%s", ws.URL, testRepo), checkoutDir)
	runOrFail(t, cloneCmd)

	// We may have cloned an 'empty' repository, 'git log' will fail in it
	logCmd := exec.Command("git", "log", "-1", "--oneline")
	logCmd.Dir = checkoutDir
	runOrFail(t, logCmd)
}

func TestAllowedShallowClone(t *testing.T) {
	// Prepare clone directory
	if err := os.RemoveAll(scratchDir); err != nil {
		t.Fatal(err)
	}

	// Prepare test server and backend
	ts := testAuthServer(nil, 200, gitOkBody(t))
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Shallow git clone (depth 1)
	cloneCmd := exec.Command("git", "clone", "--depth", "1", fmt.Sprintf("%s/%s", ws.URL, testRepo), checkoutDir)
	runOrFail(t, cloneCmd)

	// We may have cloned an 'empty' repository, 'git log' will fail in it
	logCmd := exec.Command("git", "log", "-1", "--oneline")
	logCmd.Dir = checkoutDir
	runOrFail(t, logCmd)
}

func TestDeniedClone(t *testing.T) {
	// Prepare clone directory
	if err := os.RemoveAll(scratchDir); err != nil {
		t.Fatal(err)
	}

	// Prepare test server and backend
	ts := testAuthServer(nil, 403, "Access denied")
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Do the git clone
	cloneCmd := exec.Command("git", "clone", fmt.Sprintf("%s/%s", ws.URL, testRepo), checkoutDir)
	out, err := cloneCmd.CombinedOutput()
	t.Logf("%s", out)
	if err == nil {
		t.Fatal("git clone should have failed")
	}
}

func TestAllowedPush(t *testing.T) {
	preparePushRepo(t)

	// Prepare the test server and backend
	ts := testAuthServer(nil, 200, gitOkBody(t))
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Perform the git push
	pushCmd := exec.Command("git", "push", fmt.Sprintf("%s/%s", ws.URL, testRepo), fmt.Sprintf("master:%s", newBranch()))
	pushCmd.Dir = checkoutDir
	runOrFail(t, pushCmd)
}

func TestDeniedPush(t *testing.T) {
	preparePushRepo(t)

	// Prepare the test server and backend
	ts := testAuthServer(nil, 403, "Access denied")
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Perform the git push
	pushCmd := exec.Command("git", "push", "-v", fmt.Sprintf("%s/%s", ws.URL, testRepo), fmt.Sprintf("master:%s", newBranch()))
	pushCmd.Dir = checkoutDir
	out, err := pushCmd.CombinedOutput()
	t.Logf("%s", out)
	if err == nil {
		t.Fatal("git push should have failed")
	}
}

func TestRegularProjectsAPI(t *testing.T) {
	apiResponse := "API RESPONSE"

	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(apiResponse)); err != nil {
			t.Fatalf("write upstream response: %v", err)
		}
	})
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	for _, resource := range []string{
		"/api/v3/projects/123/repository/not/special",
		"/api/v3/projects/foo%2Fbar/repository/not/special",
		"/api/v3/projects/123/not/special",
		"/api/v3/projects/foo%2Fbar/not/special",
		"/api/v3/projects/foo%2Fbar%2Fbaz/repository/not/special",
		"/api/v3/projects/foo%2Fbar%2Fbaz%2Fqux/repository/not/special",
	} {
		resp, err := http.Get(ws.URL + resource)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		buf := &bytes.Buffer{}
		if _, err := io.Copy(buf, resp.Body); err != nil {
			t.Error(err)
		}
		if buf.String() != apiResponse {
			t.Errorf("GET %q: Expected %q, got %q", resource, apiResponse, buf.String())
		}
		if resp.StatusCode != 200 {
			t.Errorf("GET %q: expected 200, got %d", resource, resp.StatusCode)
		}
		if h := resp.Header.Get(helper.NginxResponseBufferHeader); h != "" {
			t.Errorf("GET %q: Expected %s not to be present, got %q", resource, helper.NginxResponseBufferHeader, h)
		}
	}
}

func TestAllowedXSendfileDownload(t *testing.T) {
	contentFilename := "my-content"
	prepareDownloadDir(t)

	allowedXSendfileDownload(t, contentFilename, "foo/uploads/bar")
}

func TestDeniedXSendfileDownload(t *testing.T) {
	contentFilename := "my-content"
	prepareDownloadDir(t)

	deniedXSendfileDownload(t, contentFilename, "foo/uploads/bar")
}

func TestAllowedStaticFile(t *testing.T) {
	content := "PUBLIC"
	if err := setupStaticFile("static file.txt", content); err != nil {
		t.Fatalf("create public/static file.txt: %v", err)
	}

	proxied := false
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, r *http.Request) {
		proxied = true
		w.WriteHeader(404)
	})
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	for _, resource := range []string{
		"/static%20file.txt",
		"/static file.txt",
	} {
		resp, err := http.Get(ws.URL + resource)
		if err != nil {
			t.Error(err)
		}
		defer resp.Body.Close()
		buf := &bytes.Buffer{}
		if _, err := io.Copy(buf, resp.Body); err != nil {
			t.Error(err)
		}
		if buf.String() != content {
			t.Errorf("GET %q: Expected %q, got %q", resource, content, buf.String())
		}
		if resp.StatusCode != 200 {
			t.Errorf("GET %q: expected 200, got %d", resource, resp.StatusCode)
		}
		if proxied {
			t.Errorf("GET %q: should not have made it to backend", resource)
		}
		if h := resp.Header.Get(helper.NginxResponseBufferHeader); h != "no" {
			t.Errorf("GET %q: Expected %s to equal %q, got %q", resource, helper.NginxResponseBufferHeader, "no", h)
		}
	}
}

func TestStaticFileRelativeURL(t *testing.T) {
	content := "PUBLIC"
	if err := setupStaticFile("static.txt", content); err != nil {
		t.Fatalf("create public/static.txt: %v", err)
	}

	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), http.HandlerFunc(http.NotFound))
	defer ts.Close()
	backendURLString := ts.URL + "/my-relative-url"
	log.Print(backendURLString)
	ws := startWorkhorseServer(backendURLString)
	defer ws.Close()

	resource := "/my-relative-url/static.txt"
	resp, err := http.Get(ws.URL + resource)
	if err != nil {
		t.Error(err)
	}
	defer resp.Body.Close()
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, resp.Body); err != nil {
		t.Error(err)
	}
	if buf.String() != content {
		t.Errorf("GET %q: Expected %q, got %q", resource, content, buf.String())
	}
	if resp.StatusCode != 200 {
		t.Errorf("GET %q: expected 200, got %d", resource, resp.StatusCode)
	}
}

func TestAllowedPublicUploadsFile(t *testing.T) {
	content := "PRIVATE but allowed"
	if err := setupStaticFile("uploads/static file.txt", content); err != nil {
		t.Fatalf("create public/uploads/static file.txt: %v", err)
	}

	proxied := false
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, r *http.Request) {
		proxied = true
		w.Header().Add("X-Sendfile", *documentRoot+r.URL.Path)
		w.WriteHeader(200)
	})
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	for _, resource := range []string{
		"/uploads/static%20file.txt",
		"/uploads/static file.txt",
	} {
		resp, err := http.Get(ws.URL + resource)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		buf := &bytes.Buffer{}
		if _, err := io.Copy(buf, resp.Body); err != nil {
			t.Fatal(err)
		}
		if buf.String() != content {
			t.Fatalf("GET %q: Expected %q, got %q", resource, content, buf.String())
		}
		if resp.StatusCode != 200 {
			t.Fatalf("GET %q: expected 200, got %d", resource, resp.StatusCode)
		}
		if !proxied {
			t.Fatalf("GET %q: never made it to backend", resource)
		}
		if h := resp.Header.Get(helper.NginxResponseBufferHeader); h != "no" {
			t.Errorf("GET %q: Expected %s to equal %q, got %q", resource, helper.NginxResponseBufferHeader, "no", h)
		}
	}
}

func TestDeniedPublicUploadsFile(t *testing.T) {
	content := "PRIVATE"
	if err := setupStaticFile("uploads/static.txt", content); err != nil {
		t.Fatalf("create public/uploads/static.txt: %v", err)
	}

	proxied := false
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, _ *http.Request) {
		proxied = true
		w.WriteHeader(404)
	})
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	for _, resource := range []string{
		"/uploads/static.txt",
		"/uploads%2Fstatic.txt",
	} {
		resp, err := http.Get(ws.URL + resource)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		buf := &bytes.Buffer{}
		if _, err := io.Copy(buf, resp.Body); err != nil {
			t.Fatal(err)
		}
		if buf.String() == content {
			t.Fatalf("GET %q: Got private file contents which should have been blocked by upstream", resource)
		}
		if resp.StatusCode != 404 {
			t.Fatalf("GET %q: expected 404, got %d", resource, resp.StatusCode)
		}
		if !proxied {
			t.Fatalf("GET %q: never made it to backend", resource)
		}
	}
}

var sendDataHeader = "Gitlab-Workhorse-Send-Data"

func sendDataResponder(command string, literalJSON string) *httptest.Server {
	handler := func(w http.ResponseWriter, r *http.Request) {
		data := base64.URLEncoding.EncodeToString([]byte(literalJSON))
		w.Header().Set(sendDataHeader, fmt.Sprintf("%s:%s", command, data))

		// This should never be returned
		if _, err := fmt.Fprintf(w, "gibberish"); err != nil {
			panic(err)
		}

		return
	}

	return testhelper.TestServerWithHandler(regexp.MustCompile(`.`), handler)
}

func doSendDataRequest(path string, command, literalJSON string) (*http.Response, []byte, error) {
	ts := sendDataResponder(command, literalJSON)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resp, err := http.Get(ws.URL + path)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	bodyData, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, err
	}

	headerValue := resp.Header.Get(sendDataHeader)
	if headerValue != "" {
		return resp, bodyData, fmt.Errorf("%s header should not be present, but has value %q", sendDataHeader, headerValue)
	}

	return resp, bodyData, nil
}

func TestArtifactsGetSingleFile(t *testing.T) {
	// We manually created this zip file in the gitlab-workhorse Git repository
	archivePath := `testdata/artifacts-archive.zip`
	fileName := "myfile"
	fileContents := "MY FILE"
	resourcePath := `/namespace/project/builds/123/artifacts/file/` + fileName
	encodedFilename := base64.StdEncoding.EncodeToString([]byte(fileName))
	jsonParams := fmt.Sprintf(`{"Archive":"%s","Entry":"%s"}`, archivePath, encodedFilename)

	resp, body, err := doSendDataRequest(resourcePath, "artifacts-entry", jsonParams)
	if err != nil {
		t.Error(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %q: expected HTTP 200, got %d", resp.Request.URL, resp.StatusCode)
	}

	if string(body) != fileContents {
		t.Fatalf("Expected file contents %q, got %q", fileContents, body)
	}

	if h := resp.Header.Get(helper.NginxResponseBufferHeader); h != "no" {
		t.Errorf("GET %q: Expected %s to equal %q, got %q", resourcePath, helper.NginxResponseBufferHeader, "no", h)
	}
}

func TestGetGitBlob(t *testing.T) {
	blobId := "50b27c6518be44c42c4d87966ae2481ce895624c" // the LICENSE file in the test repository
	blobLength := 1075
	jsonParams := fmt.Sprintf(`{"RepoPath":"%s","BlobId":"%s"}`, path.Join(testRepoRoot, testRepo), blobId)

	resp, body, err := doSendDataRequest("/something", "git-blob", jsonParams)
	if err != nil {
		t.Error(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %q: expected HTTP 200, got %d", resp.Request.URL, resp.StatusCode)
	}

	if len(body) != blobLength {
		t.Fatalf("Expected body of %d bytes, got %d", blobLength, len(body))
	}

	if cl := resp.Header.Get("Content-Length"); cl != fmt.Sprintf("%d", blobLength) {
		t.Fatalf("Expected Content-Length %v, got %q", blobLength, cl)
	}

	if !strings.HasPrefix(string(body), "The MIT License (MIT)") {
		t.Fatalf("Expected MIT license, got %q", body)
	}

	if h := resp.Header.Get(helper.NginxResponseBufferHeader); h != "no" {
		t.Errorf("Expected %s to equal %q, got %q", helper.NginxResponseBufferHeader, "no", h)
	}
}

func TestGetGitDiff(t *testing.T) {
	fromSha := "be93687618e4b132087f430a4d8fc3a609c9b77c"
	toSha := "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51"
	jsonParams := fmt.Sprintf(`{"RepoPath":"%s","ShaFrom":"%s","ShaTo":"%s"}`, path.Join(testRepoRoot, testRepo), fromSha, toSha)

	resp, body, err := doSendDataRequest("/something", "git-diff", jsonParams)
	if err != nil {
		t.Error(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %q: expected HTTP 200, got %d", resp.Request.URL, resp.StatusCode)
	}

	if !strings.HasPrefix(string(body), "diff --git a/README b/README") {
		t.Fatalf("diff --git a/README b/README, got %q", body)
	}

	bodyLengthBytes := len(body)
	if bodyLengthBytes != 155 {
		t.Fatal("Expected the body to consist of 155 bytes, got %v", bodyLengthBytes)
	}

	if h := resp.Header.Get(helper.NginxResponseBufferHeader); h != "no" {
		t.Errorf("Expected %s to equal %q, got %q", helper.NginxResponseBufferHeader, "no", h)
	}
}

func TestGetGitPatch(t *testing.T) {
	// HEAD of master branch against HEAD of fix branch
	fromSha := "6907208d755b60ebeacb2e9dfea74c92c3449a1f"
	toSha := "48f0be4bd10c1decee6fae52f9ae6d10f77b60f4"
	jsonParams := fmt.Sprintf(`{"RepoPath":"%s","ShaFrom":"%s","ShaTo":"%s"}`, path.Join(testRepoRoot, testRepo), fromSha, toSha)

	resp, body, err := doSendDataRequest("/something", "git-format-patch", jsonParams)
	if err != nil {
		t.Error(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %q: expected HTTP 200, got %d", resp.Request.URL, resp.StatusCode)
	}

	// Only the two commits on the fix branch should be included
	testhelper.AssertPatchSeries(t, body, "12d65c8dd2b2676fa3ac47d955accc085a37a9c1", toSha)

	if h := resp.Header.Get(helper.NginxResponseBufferHeader); h != "no" {
		t.Errorf("Expected %s to equal %q, got %q", helper.NginxResponseBufferHeader, "no", h)
	}
}

func TestApiContentTypeBlock(t *testing.T) {
	wrongResponse := `{"hello":"world"}`
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", api.ResponseContentType)
		if _, err := w.Write([]byte(wrongResponse)); err != nil {
			t.Fatalf("write upstream response: %v", err)
		}
	})
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resourcePath := "/something"
	resp, err := http.Get(ws.URL + resourcePath)
	if err != nil {
		t.Error(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 500 {
		t.Errorf("GET %q: expected 500, got %d", resourcePath, resp.StatusCode)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(body), "world") {
		t.Errorf("unexpected response body: %q", body)
	}
}

func TestGetInfoRefsProxiedToGitalySuccessfully(t *testing.T) {
	apiResponse := gitOkBody(t)
	apiResponse.GitalyResourcePath = "/projects/1/git-http/info-refs"

	gitalyServer := startGitalyServer(t)
	defer func() {
		gitalyServer.Stop()
		gitaly.CloseConnections()
	}()

	apiResponse.GitalySocketPath = gitalySocketPath
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := "/gitlab-org/gitlab-test.git/info/refs?service=git-upload-pack"
	resp, err := http.Get(ws.URL + resource)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	responseBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Error(err)
	}

	expectedContent := testhelper.GitalyInfoRefsResponseMock
	if !bytes.Equal(responseBody, []byte(expectedContent)) {
		t.Errorf("GET %q: Expected %q, got %q", resource, expectedContent, responseBody)
	}
}

func TestGetInfoRefsHandledLocallyDueToEmptyGitalySocketPath(t *testing.T) {
	gitalyServer := startGitalyServer(t)
	defer func() {
		gitalyServer.Stop()
		gitaly.CloseConnections()
	}()

	apiResponse := gitOkBody(t)
	apiResponse.GitalySocketPath = ""
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := "/gitlab-org/gitlab-test.git/info/refs?service=git-upload-pack"
	resp, err := http.Get(ws.URL + resource)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	responseBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Error(err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("GET %q: expected 200, got %d", resource, resp.StatusCode)
	}

	expectedContent := testhelper.GitalyInfoRefsResponseMock
	if bytes.Contains(responseBody, []byte(expectedContent)) {
		t.Errorf("GET %q: request should not have been proxied to Gitaly", resource)
	}
}

func TestAPIFalsePositivesAreProxied(t *testing.T) {
	goodResponse := []byte(`<html></html>`)
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.RequestHeader) != "" && r.Method != "GET" {
			w.WriteHeader(500)
			w.Write([]byte("non-GET request went through PreAuthorize handler"))
		} else {
			w.Header().Set("Content-Type", "text/html")
			if _, err := w.Write(goodResponse); err != nil {
				t.Fatalf("write upstream response: %v", err)
			}
		}
	})
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Each of these cases is a specially-handled path in Workhorse that may
	// actually be a request to be sent to gitlab-rails.
	for _, tc := range []struct {
		method string
		path   string
	}{
		{"GET", "/nested/group/project/blob/master/foo.git/info/refs"},
		{"POST", "/nested/group/project/blob/master/foo.git/git-upload-pack"},
		{"POST", "/nested/group/project/blob/master/foo.git/git-receive-pack"},
		{"PUT", "/nested/group/project/blob/master/foo.git/gitlab-lfs/objects/0000000000000000000000000000000000000000000000000000000000000000/0"},
		{"GET", "/nested/group/project/blob/master/environments/1/terminal.ws"},
	} {
		req, err := http.NewRequest(tc.method, ws.URL+tc.path, nil)
		if err != nil {
			t.Logf("Creating response for %+v failed: %v", tc, err)
			t.Fail()
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("Reading response from workhorse for %+v failed: %s", tc, err)
			t.Fail()
			continue
		}
		defer resp.Body.Close()

		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Logf("Reading response from workhorse for %+v failed: %s", tc, err)
			t.Fail()
		}

		if resp.StatusCode != http.StatusOK {
			t.Logf("Expected HTTP 200 response for %+v, got %d. Body: %s", tc, resp.StatusCode, string(respBody))
			t.Fail()
		}

		if resp.Header.Get("Content-Type") != "text/html" {
			t.Logf("Unexpected response content type for %+v: %s", tc, resp.Header.Get("Content-Type"))
			t.Fail()
		}

		if bytes.Compare(respBody, goodResponse) != 0 {
			t.Logf("Unexpected response body for %+v: %s", tc, string(respBody))
			t.Fail()
		}
	}
}

func setupStaticFile(fpath, content string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	*documentRoot = path.Join(cwd, testDocumentRoot)
	if err := os.MkdirAll(path.Join(*documentRoot, path.Dir(fpath)), 0755); err != nil {
		return err
	}
	static_file := path.Join(*documentRoot, fpath)
	if err := ioutil.WriteFile(static_file, []byte(content), 0666); err != nil {
		return err
	}
	return nil
}

func prepareDownloadDir(t *testing.T) {
	if err := os.RemoveAll(scratchDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		t.Fatal(err)
	}
}

func preparePushRepo(t *testing.T) {
	if err := os.RemoveAll(scratchDir); err != nil {
		t.Fatal(err)
	}
	cloneCmd := exec.Command("git", "clone", path.Join(testRepoRoot, testRepo), checkoutDir)
	runOrFail(t, cloneCmd)
}

func newBranch() string {
	return fmt.Sprintf("branch-%d", time.Now().UnixNano())
}

func testAuthServer(url *regexp.Regexp, code int, body interface{}) *httptest.Server {
	return testhelper.TestServerWithHandler(url, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", api.ResponseContentType)

		// Write pure string
		if data, ok := body.(string); ok {
			log.Println("UPSTREAM", r.Method, r.URL, code)
			w.WriteHeader(code)
			fmt.Fprint(w, data)
			return
		}

		// Write json string
		data, err := json.Marshal(body)
		if err != nil {
			log.Println("UPSTREAM", r.Method, r.URL, "FAILURE", err)
			w.WriteHeader(503)
			fmt.Fprint(w, err)
			return
		}

		log.Println("UPSTREAM", r.Method, r.URL, code)
		w.WriteHeader(code)
		w.Write(data)
	})
}

func archiveOKServer(t *testing.T, archiveName string) *httptest.Server {
	return testhelper.TestServerWithHandler(regexp.MustCompile("."), func(w http.ResponseWriter, r *http.Request) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		archivePath := path.Join(cwd, cacheDir, archiveName)

		params := struct{ RepoPath, ArchivePath, CommitId, ArchivePrefix string }{
			repoPath(t),
			archivePath,
			"c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd",
			"foobar123",
		}
		jsonData, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		encodedJSON := base64.URLEncoding.EncodeToString(jsonData)
		w.Header().Set("Gitlab-Workhorse-Send-Data", "git-archive:"+encodedJSON)
	})
}

func newUpstreamConfig(authBackend string) *config.Config {
	return &config.Config{
		Version:      "123",
		DocumentRoot: testDocumentRoot,
		Backend:      helper.URLMustParse(authBackend),
	}
}

func startWorkhorseServer(authBackend string) *httptest.Server {
	return startWorkhorseServerWithConfig(newUpstreamConfig(authBackend))
}

func startWorkhorseServerWithConfig(cfg *config.Config) *httptest.Server {
	testhelper.ConfigureSecret()
	u := upstream.NewUpstream(*cfg)

	return httptest.NewServer(u)
}

func startGitalyServer(t *testing.T) *grpc.Server {
	server := grpc.NewServer()
	listener, err := net.Listen("unix", gitalySocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterSmartHTTPServer(server, testhelper.NewGitalyServer())

	go server.Serve(listener)

	return server
}

func runOrFail(t *testing.T, cmd *exec.Cmd) {
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatal(err)
	}
}

func gitOkBody(t *testing.T) *api.Response {
	return &api.Response{
		GL_ID:    "user-123",
		RepoPath: repoPath(t),
	}
}

func repoPath(t *testing.T) string {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return path.Join(cwd, testRepoRoot, testRepo)
}
