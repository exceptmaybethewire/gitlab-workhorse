package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/git"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/gitaly"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func TestFailedCloneNoGitaly(t *testing.T) {
	// Prepare clone directory
	require.NoError(t, os.RemoveAll(scratchDir))

	authBody := &api.Response{
		GL_ID:       "user-123",
		GL_USERNAME: "username",
		RepoPath:    repoPath(t),
		// This will create a failure to connect to Gitaly
		GitalyServer: gitaly.Server{Address: "unix:/nonexistent"},
	}

	// Prepare test server and backend
	ts := testAuthServer(nil, 200, authBody)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Do the git clone
	cloneCmd := exec.Command("git", "clone", fmt.Sprintf("%s/%s", ws.URL, testRepo), checkoutDir)
	out, err := cloneCmd.CombinedOutput()
	t.Log(string(out))
	assert.Error(t, err, "git clone should have failed")
}

func TestGetInfoRefsProxiedToGitalySuccessfully(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath

	apiResponse := gitOkBody(t)
	apiResponse.GitalyServer.Address = gitalyAddress

	for _, showAllRefs := range []bool{true, false} {
		t.Run(fmt.Sprintf("ShowAllRefs=%v", showAllRefs), func(t *testing.T) {
			apiResponse.ShowAllRefs = showAllRefs

			ts := testAuthServer(nil, 200, apiResponse)
			defer ts.Close()

			ws := startWorkhorseServer(ts.URL)
			defer ws.Close()

			resource := "/gitlab-org/gitlab-test.git/info/refs?service=git-upload-pack"
			_, body := httpGet(t, ws.URL+resource)

			expectedContent := "\n\000" + string(testhelper.GitalyInfoRefsResponseMock) + "\000"
			if showAllRefs {
				expectedContent = git.GitConfigShowAllRefs + expectedContent
			}

			assert.Equal(t, expectedContent, body, "GET %q: response body", resource)

		})
	}

}

func TestGetInfoRefsProxiedToGitalyInterruptedStream(t *testing.T) {
	apiResponse := gitOkBody(t)
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	apiResponse.GitalyServer.Address = gitalyAddress

	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := "/gitlab-org/gitlab-test.git/info/refs?service=git-upload-pack"
	resp, err := http.Get(ws.URL + resource)
	require.NoError(t, err)

	// This causes the server stream to be interrupted instead of consumed entirely.
	resp.Body.Close()

	done := make(chan struct{})
	go func() {
		gitalyServer.WaitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(10 * time.Second):
		t.Fatal("time out waiting for gitaly handler to return")
	}
}

func TestPostReceivePackProxiedToGitalySuccessfully(t *testing.T) {
	apiResponse := gitOkBody(t)

	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	apiResponse.GitalyServer.Address = "unix://" + socketPath
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := "/gitlab-org/gitlab-test.git/git-receive-pack"
	resp, body := httpPost(
		t,
		ws.URL+resource,
		"application/x-git-receive-pack-request",
		testhelper.GitalyReceivePackResponseMock,
	)

	expectedBody := strings.Join([]string{
		apiResponse.Repository.StorageName,
		apiResponse.Repository.RelativePath,
		apiResponse.GL_ID,
		apiResponse.GL_USERNAME,
		string(testhelper.GitalyReceivePackResponseMock),
	}, "\000")

	assert.Equal(t, 200, resp.StatusCode, "POST %q", resource)
	assert.Equal(t, expectedBody, body, "POST %q: response body", resource)
	testhelper.AssertResponseHeader(t, resp, "Content-Type", "application/x-git-receive-pack-result")
}

func TestPostReceivePackProxiedToGitalyInterrupted(t *testing.T) {
	apiResponse := gitOkBody(t)

	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	apiResponse.GitalyServer.Address = "unix://" + socketPath
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := "/gitlab-org/gitlab-test.git/git-receive-pack"
	resp, err := http.Post(
		ws.URL+resource,
		"application/x-git-receive-pack-request",
		bytes.NewReader(testhelper.GitalyReceivePackResponseMock),
	)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode, "POST %q", resource)

	// This causes the server stream to be interrupted instead of consumed entirely.
	resp.Body.Close()

	done := make(chan struct{})
	go func() {
		gitalyServer.WaitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(10 * time.Second):
		t.Fatal("time out waiting for gitaly handler to return")
	}
}

func TestPostUploadPackProxiedToGitalySuccessfully(t *testing.T) {
	for i, tc := range []struct {
		showAllRefs bool
		code        codes.Code
	}{
		{true, codes.OK},
		{true, codes.Unavailable},
		{false, codes.OK},
		{false, codes.Unavailable},
	} {
		t.Run(fmt.Sprintf("Case %d", i), func(t *testing.T) {
			apiResponse := gitOkBody(t)
			apiResponse.ShowAllRefs = tc.showAllRefs

			gitalyServer, socketPath := startGitalyServer(t, tc.code)
			defer gitalyServer.Stop()

			apiResponse.GitalyServer.Address = "unix://" + socketPath
			ts := testAuthServer(nil, 200, apiResponse)
			defer ts.Close()

			ws := startWorkhorseServer(ts.URL)
			defer ws.Close()

			resource := "/gitlab-org/gitlab-test.git/git-upload-pack"
			resp, body := httpPost(
				t,
				ws.URL+resource,
				"application/x-git-upload-pack-request",
				testhelper.GitalyUploadPackResponseMock,
			)

			expectedBodyParts := []string{
				apiResponse.Repository.StorageName,
				apiResponse.Repository.RelativePath,
			}
			if tc.showAllRefs {
				expectedBodyParts = append(expectedBodyParts, git.GitConfigShowAllRefs+"\n")
			} else {
				expectedBodyParts = append(expectedBodyParts, "\n")
			}

			expectedBodyParts = append(expectedBodyParts, string(testhelper.GitalyUploadPackResponseMock))
			expectedBody := strings.Join(expectedBodyParts, "\000")

			assert.Equal(t, 200, resp.StatusCode, "POST %q", resource)
			assert.Equal(t, expectedBody, body, "POST %q: response body", resource)
			testhelper.AssertResponseHeader(t, resp, "Content-Type", "application/x-git-upload-pack-result")
		})
	}
}

func TestPostUploadPackProxiedToGitalyInterrupted(t *testing.T) {
	apiResponse := gitOkBody(t)

	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	apiResponse.GitalyServer.Address = "unix://" + socketPath
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := "/gitlab-org/gitlab-test.git/git-upload-pack"
	resp, err := http.Post(
		ws.URL+resource,
		"application/x-git-upload-pack-request",
		bytes.NewReader(testhelper.GitalyUploadPackResponseMock),
	)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode, "POST %q", resource)

	// This causes the server stream to be interrupted instead of consumed entirely.
	resp.Body.Close()

	done := make(chan struct{})
	go func() {
		gitalyServer.WaitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(10 * time.Second):
		t.Fatal("time out waiting for gitaly handler to return")
	}
}

func TestGetInfoRefsHandledLocallyDueToEmptyGitalySocketPath(t *testing.T) {
	gitalyServer, _ := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	apiResponse := gitOkBody(t)
	apiResponse.GitalyServer.Address = ""
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := "/gitlab-org/gitlab-test.git/info/refs?service=git-upload-pack"
	resp, body := httpGet(t, ws.URL+resource)

	assert.Equal(t, 200, resp.StatusCode, "GET %q", resource)
	assert.NotContains(t, string(testhelper.GitalyInfoRefsResponseMock), body, "GET %q: should not have been proxied to Gitaly", resource)
	testhelper.AssertResponseHeader(t, resp, "Content-Type", "application/x-git-upload-pack-advertisement")
}

func TestPostReceivePackHandledLocallyDueToEmptyGitalySocketPath(t *testing.T) {
	gitalyServer, _ := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	apiResponse := gitOkBody(t)
	apiResponse.GitalyServer.Address = ""
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := "/gitlab-org/gitlab-test.git/git-receive-pack"
	payload := []byte("This payload should not reach Gitaly")
	resp, body := httpPost(t, ws.URL+resource, "application/x-git-receive-pack-request", payload)

	assert.Equal(t, 200, resp.StatusCode, "POST %q: status code", resource)
	assert.NotContains(t, payload, body, "POST %q: request should not have been proxied to Gitaly", resource)
	testhelper.AssertResponseHeader(t, resp, "Content-Type", "application/x-git-receive-pack-result")
}

func TestPostUploadPackHandledLocallyDueToEmptyGitalySocketPath(t *testing.T) {
	gitalyServer, _ := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	apiResponse := gitOkBody(t)
	apiResponse.GitalyServer.Address = ""
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := "/gitlab-org/gitlab-test.git/git-upload-pack"
	payload := []byte("This payload should not reach Gitaly")
	resp, body := httpPost(t, ws.URL+resource, "application/x-git-upload-pack-request", payload)

	assert.Equal(t, 200, resp.StatusCode, "POST %q: status code", resource)
	assert.NotContains(t, payload, body, "POST %q: request should not have been proxied to Gitaly", resource)
	testhelper.AssertResponseHeader(t, resp, "Content-Type", "application/x-git-upload-pack-result")
}

func TestGetBlobProxiedToGitalySuccessfully(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	repoStorage := "default"
	oid := "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51"
	repoRelativePath := "foo/bar.git"
	jsonParams := fmt.Sprintf(`{"GitalyServer":{"Address":"%s","Token":""},"GetBlobRequest":{"repository":{"storage_name":"%s","relative_path":"%s"},"oid":"%s","limit":-1}}`,
		gitalyAddress, repoStorage, repoRelativePath, oid)
	expectedBody := testhelper.GitalyGetBlobResponseMock
	blobLength := len(expectedBody)

	resp, body, err := doSendDataRequest("/something", "git-blob", jsonParams)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resp.Request.URL)
	assert.Equal(t, expectedBody, string(body), "GET %q: response body", resp.Request.URL)
	assert.Equal(t, blobLength, len(body), "GET %q: body size", resp.Request.URL)
	testhelper.AssertResponseHeader(t, resp, "Content-Length", strconv.Itoa(blobLength))
}

func TestGetDiffProxiedToGitalySuccessfully(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	repoStorage := "default"
	rightCommit := "e395f646b1499e8e0279445fc99a0596a65fab7e"
	leftCommit := "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"
	repoRelativePath := "foo/bar.git"
	jsonParams := fmt.Sprintf(`{"GitalyServer":{"Address":"%s","Token":""},"RawDiffRequest":"{\"repository\":{\"storageName\":\"%s\",\"relativePath\":\"%s\"},\"rightCommitId\":\"%s\",\"leftCommitId\":\"%s\"}"}`,
		gitalyAddress, repoStorage, repoRelativePath, leftCommit, rightCommit)
	expectedBody := testhelper.GitalyGetDiffResponseMock

	resp, body, err := doSendDataRequest("/something", "git-diff", jsonParams)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resp.Request.URL)
	assert.Equal(t, expectedBody, string(body), "GET %q: response body", resp.Request.URL)
}

func TestGetPatchProxiedToGitalySuccessfully(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	repoStorage := "default"
	rightCommit := "e395f646b1499e8e0279445fc99a0596a65fab7e"
	leftCommit := "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"
	repoRelativePath := "foo/bar.git"
	jsonParams := fmt.Sprintf(`{"GitalyServer":{"Address":"%s","Token":""},"RawPatchRequest":"{\"repository\":{\"storageName\":\"%s\",\"relativePath\":\"%s\"},\"rightCommitId\":\"%s\",\"leftCommitId\":\"%s\"}"}`,
		gitalyAddress, repoStorage, repoRelativePath, leftCommit, rightCommit)
	expectedBody := testhelper.GitalyGetPatchResponseMock

	resp, body, err := doSendDataRequest("/something", "git-format-patch", jsonParams)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resp.Request.URL)
	assert.Equal(t, expectedBody, string(body), "GET %q: response body", resp.Request.URL)
}

func TestGetBlobProxiedToGitalyInterruptedStream(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	repoStorage := "default"
	oid := "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51"
	repoRelativePath := "foo/bar.git"
	jsonParams := fmt.Sprintf(`{"GitalyServer":{"Address":"%s","Token":""},"GetBlobRequest":{"repository":{"storage_name":"%s","relative_path":"%s"},"oid":"%s","limit":-1}}`,
		gitalyAddress, repoStorage, repoRelativePath, oid)

	resp, _, err := doSendDataRequest("/something", "git-blob", jsonParams)
	require.NoError(t, err)

	// This causes the server stream to be interrupted instead of consumed entirely.
	resp.Body.Close()

	done := make(chan struct{})
	go func() {
		gitalyServer.WaitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(10 * time.Second):
		t.Fatal("time out waiting for gitaly handler to return")
	}
}

func TestGetArchiveProxiedToGitalySuccessfully(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	repoStorage := "default"
	oid := "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51"
	repoRelativePath := "foo/bar.git"
	archivePrefix := "repo-1"
	expectedBody := testhelper.GitalyGetArchiveResponseMock
	archiveLength := len(expectedBody)

	testCases := []struct {
		archivePath   string
		cacheDisabled bool
	}{
		{archivePath: path.Join(scratchDir, "my/path"), cacheDisabled: false},
		{archivePath: "/var/empty/my/path", cacheDisabled: true},
	}

	for _, tc := range testCases {
		jsonParams := fmt.Sprintf(`{"GitalyServer":{"Address":"%s","Token":""},"GitalyRepository":{"storage_name":"%s","relative_path":"%s"},"ArchivePath":"%s","ArchivePrefix":"%s","CommitId":"%s","DisableCache":%v}`,
			gitalyAddress, repoStorage, repoRelativePath, tc.archivePath, archivePrefix, oid, tc.cacheDisabled)
		resp, body, err := doSendDataRequest("/archive.tar.gz", "git-archive", jsonParams)
		require.NoError(t, err)

		assert.Equal(t, 200, resp.StatusCode, "GET %q: status code", resp.Request.URL)
		assert.Equal(t, expectedBody, string(body), "GET %q: response body", resp.Request.URL)
		assert.Equal(t, archiveLength, len(body), "GET %q: body size", resp.Request.URL)

		if tc.cacheDisabled {
			_, err := os.Stat(tc.archivePath)
			require.True(t, os.IsNotExist(err), "expected 'does not exist', got: %v", err)
		} else {
			cachedArchive, err := ioutil.ReadFile(tc.archivePath)
			require.NoError(t, err)
			require.Equal(t, expectedBody, string(cachedArchive))
		}
	}
}

func TestGetArchiveProxiedToGitalyInterruptedStream(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	repoStorage := "default"
	oid := "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51"
	repoRelativePath := "foo/bar.git"
	archivePath := "my/path"
	archivePrefix := "repo-1"
	jsonParams := fmt.Sprintf(`{"GitalyServer":{"Address":"%s","Token":""},"GitalyRepository":{"storage_name":"%s","relative_path":"%s"},"ArchivePath":"%s","ArchivePrefix":"%s","CommitId":"%s"}`,
		gitalyAddress, repoStorage, repoRelativePath, path.Join(scratchDir, archivePath), archivePrefix, oid)

	resp, _, err := doSendDataRequest("/archive.tar.gz", "git-archive", jsonParams)
	require.NoError(t, err)

	// This causes the server stream to be interrupted instead of consumed entirely.
	resp.Body.Close()

	done := make(chan struct{})
	go func() {
		gitalyServer.WaitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(10 * time.Second):
		t.Fatal("time out waiting for gitaly handler to return")
	}
}

func TestGetDiffProxiedToGitalyInterruptedStream(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	repoStorage := "default"
	rightCommit := "e395f646b1499e8e0279445fc99a0596a65fab7e"
	leftCommit := "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"
	repoRelativePath := "foo/bar.git"
	jsonParams := fmt.Sprintf(`{"GitalyServer":{"Address":"%s","Token":""},"RawDiffRequest":"{\"repository\":{\"storageName\":\"%s\",\"relativePath\":\"%s\"},\"rightCommitId\":\"%s\",\"leftCommitId\":\"%s\"}"}`,
		gitalyAddress, repoStorage, repoRelativePath, leftCommit, rightCommit)

	resp, _, err := doSendDataRequest("/something", "git-diff", jsonParams)
	require.NoError(t, err)

	// This causes the server stream to be interrupted instead of consumed entirely.
	resp.Body.Close()

	done := make(chan struct{})
	go func() {
		gitalyServer.WaitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(10 * time.Second):
		t.Fatal("time out waiting for gitaly handler to return")
	}
}

func TestGetPatchProxiedToGitalyInterruptedStream(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	repoStorage := "default"
	rightCommit := "e395f646b1499e8e0279445fc99a0596a65fab7e"
	leftCommit := "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"
	repoRelativePath := "foo/bar.git"
	jsonParams := fmt.Sprintf(`{"GitalyServer":{"Address":"%s","Token":""},"RawPatchRequest":"{\"repository\":{\"storageName\":\"%s\",\"relativePath\":\"%s\"},\"rightCommitId\":\"%s\",\"leftCommitId\":\"%s\"}"}`,
		gitalyAddress, repoStorage, repoRelativePath, leftCommit, rightCommit)

	resp, _, err := doSendDataRequest("/something", "git-format-patch", jsonParams)
	require.NoError(t, err)

	// This causes the server stream to be interrupted instead of consumed entirely.
	resp.Body.Close()

	done := make(chan struct{})
	go func() {
		gitalyServer.WaitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(10 * time.Second):
		t.Fatal("time out waiting for gitaly handler to return")
	}
}

type combinedServer struct {
	*grpc.Server
	*testhelper.GitalyTestServer
}

func startGitalyServer(t *testing.T, finalMessageCode codes.Code) (*combinedServer, string) {
	socketPath := path.Join(scratchDir, fmt.Sprintf("gitaly-%d.sock", rand.Int()))
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	gitalyServer := testhelper.NewGitalyServer(finalMessageCode)
	pb.RegisterSmartHTTPServiceServer(server, gitalyServer)
	pb.RegisterBlobServiceServer(server, gitalyServer)
	pb.RegisterRepositoryServiceServer(server, gitalyServer)
	pb.RegisterDiffServiceServer(server, gitalyServer)

	go server.Serve(listener)

	return &combinedServer{Server: server, GitalyTestServer: gitalyServer}, socketPath
}
