package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/git"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/gitaly"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/upstream"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scratchDir = "testdata/scratch"
const testRepoRoot = "testdata/data"
const testDocumentRoot = "testdata/public"
const testRepo = "group/test.git"
const testProject = "group/test"

var checkoutDir = path.Join(scratchDir, "test")
var cacheDir = path.Join(scratchDir, "cache")

func TestMain(m *testing.M) {
	git.Testing = true

	if _, err := os.Stat(path.Join(testRepoRoot, testRepo)); os.IsNotExist(err) {
		log.Fatal("cannot find test repository. Please run 'make prepare-tests'")
	}

	if err := testhelper.BuildExecutables(); err != nil {
		log.WithError(err).Fatal()
	}

	defer gitaly.CloseConnections()

	os.Exit(m.Run())
}

func TestAllowedClone(t *testing.T) {
	// Prepare clone directory
	require.NoError(t, os.RemoveAll(scratchDir))

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
	require.NoError(t, os.RemoveAll(scratchDir))

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
	require.NoError(t, os.RemoveAll(scratchDir))

	// Prepare test server and backend
	ts := testAuthServer(nil, 403, "Access denied")
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Do the git clone
	cloneCmd := exec.Command("git", "clone", fmt.Sprintf("%s/%s", ws.URL, testRepo), checkoutDir)
	out, err := cloneCmd.CombinedOutput()
	t.Log(string(out))
	assert.Error(t, err, "git clone should have failed")
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
	t.Log(string(out))
	assert.Error(t, err, "git push should have failed")
}

func TestRegularProjectsAPI(t *testing.T) {
	apiResponse := "API RESPONSE"

	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(apiResponse))
		require.NoError(t, err)
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
		resp, body := httpGet(t, ws.URL+resource, nil)

		assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resource)
		assert.Equal(t, apiResponse, body, "GET %q: response body", resource)
		assertNginxResponseBuffering(t, "", resp, "GET %q: nginx response buffering", resource)
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
	require.NoError(t, setupStaticFile("static file.txt", content))

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
		resp, body := httpGet(t, ws.URL+resource, nil)

		assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resource)
		assert.Equal(t, content, body, "GET %q: response body", resource)
		assertNginxResponseBuffering(t, "no", resp, "GET %q: nginx response buffering", resource)
		assert.False(t, proxied, "GET %q: should not have made it to backend", resource)
	}
}

func TestStaticFileRelativeURL(t *testing.T) {
	content := "PUBLIC"
	require.NoError(t, setupStaticFile("static.txt", content), "create public/static.txt")

	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), http.HandlerFunc(http.NotFound))
	defer ts.Close()
	backendURLString := ts.URL + "/my-relative-url"
	log.Print(backendURLString)
	ws := startWorkhorseServer(backendURLString)
	defer ws.Close()

	resource := "/my-relative-url/static.txt"
	resp, body := httpGet(t, ws.URL+resource, nil)

	assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resource)
	assert.Equal(t, content, body, "GET %q: response body", resource)
}

func TestAllowedPublicUploadsFile(t *testing.T) {
	content := "PRIVATE but allowed"
	require.NoError(t, setupStaticFile("uploads/static file.txt", content), "create public/uploads/static file.txt")

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
		resp, body := httpGet(t, ws.URL+resource, nil)

		assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resource)
		assert.Equal(t, content, body, "GET %q: response body", resource)
		assert.True(t, proxied, "GET %q: never made it to backend", resource)
	}
}

func TestDeniedPublicUploadsFile(t *testing.T) {
	content := "PRIVATE"
	require.NoError(t, setupStaticFile("uploads/static.txt", content), "create public/uploads/static.txt")

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
		resp, body := httpGet(t, ws.URL+resource, nil)

		assert.Equal(t, 404, resp.StatusCode, "GET %q: status code", resource)
		assert.Equal(t, "", body, "GET %q: response body", resource)
		assert.True(t, proxied, "GET %q: never made it to backend", resource)
	}
}

func TestStaticErrorPage(t *testing.T) {
	errorPageBody := `<html>
<body>
This is a static error page for code 499
</body>
</html>
`
	require.NoError(t, setupStaticFile("499.html", errorPageBody))
	ts := testhelper.TestServerWithHandler(nil, func(w http.ResponseWriter, _ *http.Request) {
		upstreamError := "499"
		// This is the point of the test: the size of the upstream response body
		// should be overridden.
		require.NotEqual(t, len(upstreamError), len(errorPageBody))
		w.WriteHeader(499)
		_, err := w.Write([]byte(upstreamError))
		require.NoError(t, err)
	})
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resourcePath := "/error-499"
	resp, body := httpGet(t, ws.URL+resourcePath, nil)

	assert.Equal(t, 499, resp.StatusCode, "GET %q: status code", resourcePath)
	assert.Equal(t, string(errorPageBody), body, "GET %q: response body", resourcePath)
}

func TestGzipAssets(t *testing.T) {
	path := "/assets/static.txt"
	content := "asset"
	require.NoError(t, setupStaticFile(path, content))

	buf := &bytes.Buffer{}
	gzipWriter := gzip.NewWriter(buf)
	_, err := gzipWriter.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, gzipWriter.Close())
	contentGzip := buf.String()
	require.NoError(t, setupStaticFile(path+".gz", contentGzip))

	proxied := false
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, r *http.Request) {
		proxied = true
		w.WriteHeader(404)
	})
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	testCases := []struct {
		content         string
		path            string
		acceptEncoding  string
		contentEncoding string
	}{
		{content: content, path: path},
		{content: contentGzip, path: path, acceptEncoding: "gzip", contentEncoding: "gzip"},
		{content: contentGzip, path: path, acceptEncoding: "gzip, compress, br", contentEncoding: "gzip"},
		{content: contentGzip, path: path, acceptEncoding: "br;q=1.0, gzip;q=0.8, *;q=0.1", contentEncoding: "gzip"},
	}

	for _, tc := range testCases {
		desc := fmt.Sprintf("accept-encoding: %q", tc.acceptEncoding)
		req, err := http.NewRequest("GET", ws.URL+tc.path, nil)
		require.NoError(t, err, desc)
		req.Header.Set("Accept-Encoding", tc.acceptEncoding)

		resp, err := http.DefaultTransport.RoundTrip(req)
		require.NoError(t, err, desc)
		defer resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err, desc)

		assert.Equal(t, 200, resp.StatusCode, "%s: status code", desc)
		assert.Equal(t, tc.content, string(b), "%s: response body", desc)
		assert.Equal(t, tc.contentEncoding, resp.Header.Get("Content-Encoding"), "%s: response body", desc)
		assert.False(t, proxied, "%s: should not have made it to backend", desc)
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
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resourcePath)
	assert.Equal(t, fileContents, string(body), "GET %q: response body", resourcePath)
	assertNginxResponseBuffering(t, "no", resp, "GET %q: nginx response buffering", resourcePath)
}

func TestSendURLForArtifacts(t *testing.T) {
	fileContents := "12345678901234567890\n"

	server := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	defer server.Close()

	// We manually created this txt file in the gitlab-workhorse Git repository
	url := server.URL + "/test-file.txt"
	jsonParams := fmt.Sprintf(`{"URL":%q}`, url)

	resourcePath := `/namespace/project/builds/123/artifacts/file/download`
	resp, body, err := doSendDataRequest(resourcePath, "send-url", jsonParams)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resourcePath)
	assert.Equal(t, fileContents, string(body), "GET %q: response body", resourcePath)
}

func TestGetGitBlob(t *testing.T) {
	blobID := "50b27c6518be44c42c4d87966ae2481ce895624c" // the LICENSE file in the test repository
	blobLength := 1075
	jsonParams := fmt.Sprintf(`{"RepoPath":"%s","BlobId":"%s"}`, path.Join(testRepoRoot, testRepo), blobID)
	expectedBody := "The MIT License (MIT)"

	resp, body, err := doSendDataRequest("/something", "git-blob", jsonParams)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resp.Request.URL)
	assert.Equal(t, expectedBody, string(body[:len(expectedBody)]), "GET %q: response body", resp.Request.URL)
	assert.Equal(t, blobLength, len(body), "GET %q: body size", resp.Request.URL)
	testhelper.AssertResponseHeader(t, resp, "Content-Length", strconv.Itoa(blobLength))
	assertNginxResponseBuffering(t, "no", resp, "GET %q: nginx response buffering", resp.Request.URL)
}

func TestGetGitDiff(t *testing.T) {
	fromSha := "be93687618e4b132087f430a4d8fc3a609c9b77c"
	toSha := "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51"
	jsonParams := fmt.Sprintf(`{"RepoPath":"%s","ShaFrom":"%s","ShaTo":"%s"}`, path.Join(testRepoRoot, testRepo), fromSha, toSha)
	expectedBody := "diff --git a/README b/README"

	resp, body, err := doSendDataRequest("/something", "git-diff", jsonParams)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resp.Request.URL)
	assert.Equal(t, expectedBody, string(body[:len(expectedBody)]), "GET %q: response body", resp.Request.URL)
	assert.Equal(t, 155, len(body), "GET %q: body size", resp.Request.URL)
	assertNginxResponseBuffering(t, "no", resp, "GET %q: nginx response buffering", resp.Request.URL)
}

func TestGetGitPatch(t *testing.T) {
	// HEAD of master branch against HEAD of fix branch
	fromSha := "6907208d755b60ebeacb2e9dfea74c92c3449a1f"
	toSha := "48f0be4bd10c1decee6fae52f9ae6d10f77b60f4"
	jsonParams := fmt.Sprintf(`{"RepoPath":"%s","ShaFrom":"%s","ShaTo":"%s"}`, path.Join(testRepoRoot, testRepo), fromSha, toSha)

	resp, body, err := doSendDataRequest("/something", "git-format-patch", jsonParams)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resp.Request.URL)
	assertNginxResponseBuffering(t, "no", resp, "GET %q: nginx response buffering", resp.Request.URL)

	// Only the two commits on the fix branch should be included
	testhelper.AssertPatchSeries(t, body, "12d65c8dd2b2676fa3ac47d955accc085a37a9c1", toSha)
}

func TestApiContentTypeBlock(t *testing.T) {
	wrongResponse := `{"hello":"world"}`
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", api.ResponseContentType)
		_, err := w.Write([]byte(wrongResponse))
		require.NoError(t, err, "write upstream response")
	})
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resourcePath := "/something"
	resp, body := httpGet(t, ws.URL+resourcePath, nil)

	assert.Equal(t, 500, resp.StatusCode, "GET %q: status code", resourcePath)
	assert.NotContains(t, wrongResponse, body, "GET %q: response body", resourcePath)
}

func TestAPIFalsePositivesAreProxied(t *testing.T) {
	goodResponse := []byte(`<html></html>`)
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.RequestHeader) != "" && r.Method != "GET" {
			w.WriteHeader(500)
			w.Write([]byte("non-GET request went through PreAuthorize handler"))
		} else {
			w.Header().Set("Content-Type", "text/html")
			_, err := w.Write(goodResponse)
			require.NoError(t, err)
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
		if !assert.NoError(t, err, "Constructing %s %q", tc.method, tc.path) {
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if !assert.NoError(t, err, "%s %q", tc.method, tc.path) {
			continue
		}
		defer resp.Body.Close()

		respBody, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err, "%s %q: reading body", tc.method, tc.path)

		assert.Equal(t, 200, resp.StatusCode, "%s %q: status code", tc.method, tc.path)
		testhelper.AssertResponseHeader(t, resp, "Content-Type", "text/html")
		assert.Equal(t, string(goodResponse), string(respBody), "%s %q: response body", tc.method, tc.path)
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
	staticFile := path.Join(*documentRoot, fpath)
	return ioutil.WriteFile(staticFile, []byte(content), 0666)
}

func prepareDownloadDir(t *testing.T) {
	require.NoError(t, os.RemoveAll(scratchDir))
	require.NoError(t, os.MkdirAll(scratchDir, 0755))
}

func preparePushRepo(t *testing.T) {
	require.NoError(t, os.RemoveAll(scratchDir))
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
		require.NoError(t, err)

		archivePath := path.Join(cwd, cacheDir, archiveName)

		params := struct{ RepoPath, ArchivePath, CommitID, ArchivePrefix string }{
			repoPath(t),
			archivePath,
			"c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd",
			"foobar123",
		}
		jsonData, err := json.Marshal(params)
		require.NoError(t, err)

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

func runOrFail(t *testing.T, cmd *exec.Cmd) {
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	require.NoError(t, err)
}

func gitOkBody(t *testing.T) *api.Response {
	repoPath := repoPath(t)
	return &api.Response{
		GL_ID:       "user-123",
		GL_USERNAME: "username",
		RepoPath:    repoPath,
		Repository: pb.Repository{
			StorageName:  "default",
			RelativePath: "foo/bar.git",
		},
	}
}

func repoPath(t *testing.T) string {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	return path.Join(cwd, testRepoRoot, testRepo)
}

func httpGet(t *testing.T, url string, headers map[string]string) (*http.Response, string) {
	req, err := http.NewRequest("GET", url, nil)
	require.NoError(t, err)

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp, string(b)
}

func httpPost(t *testing.T, url string, headers map[string]string, reqBody []byte) (*http.Response, string) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	require.NoError(t, err)

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp, string(b)
}

func assertNginxResponseBuffering(t *testing.T, expected string, resp *http.Response, msgAndArgs ...interface{}) {
	actual := resp.Header.Get(helper.NginxResponseBufferHeader)
	assert.Equal(t, expected, actual, msgAndArgs...)
}
