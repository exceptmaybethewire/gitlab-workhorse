package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
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
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/upstream"
)

const scratchDir = "testdata/scratch"
const testRepoRoot = "testdata/data"
const testDocumentRoot = "testdata/public"
const testRepo = "group/test.git"
const testProject = "group/test"

var checkoutDir = path.Join(scratchDir, "test")
var cacheDir = path.Join(scratchDir, "cache")

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

func TestAllowedDownloadZip(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s/repository/archive.zip", ws.URL, testProject))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("unzip", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedDownloadTar(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.tar"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s/repository/archive.tar", ws.URL, testProject))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("tar", "xf", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedDownloadTarGz(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.tar.gz"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s/repository/archive.tar.gz", ws.URL, testProject))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("tar", "zxf", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedDownloadTarBz2(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.tar.bz2"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s/repository/archive.tar.bz2", ws.URL, testProject))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("tar", "jxf", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedApiDownloadZip(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/api/v3/projects/123/repository/archive.zip", ws.URL))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("unzip", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedApiDownloadZipWithSlash(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Use foo%2Fbar instead of a numeric ID
	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/api/v3/projects/foo%%2Fbar/repository/archive.zip", ws.URL))
	if !strings.Contains(downloadCmd.Args[3], `projects/foo%2Fbar/repository`) {
		t.Fatalf("Cannot find percent-2F: %v", downloadCmd.Args)
	}
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("unzip", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestDownloadCacheHit(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}
	cachedContent := []byte("cached")
	if err := ioutil.WriteFile(path.Join(cacheDir, archiveName), cachedContent, 0644); err != nil {
		t.Fatal(err)
	}

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/api/v3/projects/123/repository/archive.zip", ws.URL))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	actual, err := ioutil.ReadFile(path.Join(scratchDir, archiveName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(actual, cachedContent) != 0 {
		t.Fatal("Unexpected file contents in download")
	}
}

func TestDownloadCacheCreate(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/api/v3/projects/123/repository/archive.zip", ws.URL))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	compareCmd := exec.Command("cmp", path.Join(cacheDir, archiveName), path.Join(scratchDir, archiveName))
	if err := compareCmd.Run(); err != nil {
		t.Fatalf("Comparison between downloaded file and cache item failed: %s", err)
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

func TestArtifactsUpload(t *testing.T) {
	reqBody := &bytes.Buffer{}
	writer := multipart.NewWriter(reqBody)
	file, err := writer.CreateFormFile("file", "my.file")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(file, "SHOULD BE ON DISK, NOT IN MULTIPART")
	writer.Close()

	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/authorize") {
			w.Header().Set("Content-Type", api.ResponseContentType)
			if _, err := fmt.Fprintf(w, `{"TempPath":"%s"}`, scratchDir); err != nil {
				t.Fatal(err)
			}
			return
		}
		err := r.ParseMultipartForm(100000)
		if err != nil {
			t.Fatal(err)
		}
		nValues := 2 // filename + path for just the upload (no metadata because we are not POSTing a valid zip file)
		if len(r.MultipartForm.Value) != nValues {
			t.Errorf("Expected to receive exactly %d values", nValues)
		}
		if len(r.MultipartForm.File) != 0 {
			t.Error("Expected to not receive any files")
		}
		w.WriteHeader(200)
	})
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := `/ci/api/v1/builds/123/artifacts`
	resp, err := http.Post(ws.URL+resource, writer.FormDataContentType(), reqBody)
	if err != nil {
		t.Error(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET %q: expected 200, got %d", resource, resp.StatusCode)
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

func startWorkhorseServer(authBackend string) *httptest.Server {
	config := upstream.Config{
		Backend:      helper.URLMustParse(authBackend),
		Version:      "123",
		SecretPath:   testhelper.SecretPath(),
		DocumentRoot: testDocumentRoot,
	}

	u := upstream.NewUpstream(config)
	return httptest.NewServer(u)
}

func runOrFail(t *testing.T, cmd *exec.Cmd) {
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatal(err)
	}
}

func gitOkBody(t *testing.T) interface{} {
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
