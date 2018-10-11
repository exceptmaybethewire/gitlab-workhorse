package main

import (
	"bytes"
	"encoding/json"
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

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
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

	testCases := []struct {
		showAllRefs bool
		gitRpc      string
	}{
		{showAllRefs: false, gitRpc: "git-upload-pack"},
		{showAllRefs: true, gitRpc: "git-upload-pack"},
		{showAllRefs: false, gitRpc: "git-receive-pack"},
		{showAllRefs: true, gitRpc: "git-receive-pack"},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("ShowAllRefs=%v,gitRpc=%v", tc.showAllRefs, tc.gitRpc), func(t *testing.T) {
			apiResponse.ShowAllRefs = tc.showAllRefs

			ts := testAuthServer(nil, 200, apiResponse)
			defer ts.Close()

			ws := startWorkhorseServer(ts.URL)
			defer ws.Close()

			gitProtocol := "fake git protocol"
			resource := "/gitlab-org/gitlab-test.git/info/refs?service=" + tc.gitRpc
			resp, body := httpGet(t, ws.URL+resource, map[string]string{"Git-Protocol": gitProtocol})

			require.Equal(t, 200, resp.StatusCode)

			bodySplit := strings.SplitN(body, "\000", 3)
			require.Len(t, bodySplit, 3)

			gitalyRequest := &pb.InfoRefsRequest{}
			require.NoError(t, jsonpb.UnmarshalString(bodySplit[0], gitalyRequest))

			require.Equal(t, gitProtocol, gitalyRequest.GitProtocol)
			if tc.showAllRefs {
				require.Equal(t, []string{git.GitConfigShowAllRefs}, gitalyRequest.GitConfigOptions)
			} else {
				require.Empty(t, gitalyRequest.GitConfigOptions)
			}

			require.Equal(t, tc.gitRpc, bodySplit[1])

			require.Equal(t, string(testhelper.GitalyInfoRefsResponseMock), bodySplit[2], "GET %q: response body", resource)
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
	apiResponse.GitConfigOptions = []string{"git-config-hello=world"}
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	gitProtocol := "fake Git protocol"
	resource := "/gitlab-org/gitlab-test.git/git-receive-pack"
	resp, body := httpPost(
		t,
		ws.URL+resource,
		map[string]string{
			"Content-Type": "application/x-git-receive-pack-request",
			"Git-Protocol": gitProtocol,
		},
		testhelper.GitalyReceivePackResponseMock,
	)

	split := strings.SplitN(body, "\000", 2)
	require.Len(t, split, 2)

	gitalyRequest := &pb.PostReceivePackRequest{}
	require.NoError(t, jsonpb.UnmarshalString(split[0], gitalyRequest))

	assert.Equal(t, apiResponse.Repository.StorageName, gitalyRequest.Repository.StorageName)
	assert.Equal(t, apiResponse.Repository.RelativePath, gitalyRequest.Repository.RelativePath)
	assert.Equal(t, apiResponse.GL_ID, gitalyRequest.GlId)
	assert.Equal(t, apiResponse.GL_USERNAME, gitalyRequest.GlUsername)
	assert.Equal(t, apiResponse.GitConfigOptions, gitalyRequest.GitConfigOptions)
	assert.Equal(t, gitProtocol, gitalyRequest.GitProtocol)

	assert.Equal(t, 200, resp.StatusCode, "POST %q", resource)
	require.Equal(t, string(testhelper.GitalyReceivePackResponseMock), split[1])
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

			gitProtocol := "fake git protocol"
			resource := "/gitlab-org/gitlab-test.git/git-upload-pack"
			resp, body := httpPost(
				t,
				ws.URL+resource,
				map[string]string{
					"Content-Type": "application/x-git-upload-pack-request",
					"Git-Protocol": gitProtocol,
				},
				testhelper.GitalyUploadPackResponseMock,
			)

			require.Equal(t, 200, resp.StatusCode, "POST %q", resource)
			testhelper.AssertResponseHeader(t, resp, "Content-Type", "application/x-git-upload-pack-result")

			bodySplit := strings.SplitN(body, "\000", 2)
			require.Len(t, bodySplit, 2)

			gitalyRequest := &pb.PostUploadPackRequest{}
			require.NoError(t, jsonpb.UnmarshalString(bodySplit[0], gitalyRequest))

			require.Equal(t, apiResponse.Repository.StorageName, gitalyRequest.Repository.StorageName)
			require.Equal(t, apiResponse.Repository.RelativePath, gitalyRequest.Repository.RelativePath)
			require.Equal(t, gitProtocol, gitalyRequest.GitProtocol)

			if tc.showAllRefs {
				require.Equal(t, []string{git.GitConfigShowAllRefs}, gitalyRequest.GitConfigOptions)
			} else {
				require.Empty(t, gitalyRequest.GitConfigOptions)
			}

			require.Equal(t, string(testhelper.GitalyUploadPackResponseMock), bodySplit[1], "POST %q: response body", resource)
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

func TestGetSnapshotProxiedToGitalySuccessfully(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath
	expectedBody := testhelper.GitalyGetSnapshotResponseMock
	archiveLength := len(expectedBody)

	params := buildGetSnapshotParams(gitalyAddress, buildPbRepo("default", "foo/bar.git"))
	resp, body, err := doSendDataRequest("/api/v4/projects/:id/snapshot", "git-snapshot", params)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode, "GET %q: status code", resp.Request.URL)
	assert.Equal(t, expectedBody, string(body), "GET %q: body", resp.Request.URL)
	assert.Equal(t, archiveLength, len(body), "GET %q: body size", resp.Request.URL)

	testhelper.AssertResponseHeader(t, resp, "Content-Disposition", `attachment; filename="snapshot.tar"`)
	testhelper.AssertResponseHeader(t, resp, "Content-Type", "application/x-tar")
	testhelper.AssertResponseHeader(t, resp, "Content-Transfer-Encoding", "binary")
	testhelper.AssertResponseHeader(t, resp, "Cache-Control", "private")
}

func TestGetSnapshotProxiedToGitalyInterruptedStream(t *testing.T) {
	gitalyServer, socketPath := startGitalyServer(t, codes.OK)
	defer gitalyServer.Stop()

	gitalyAddress := "unix://" + socketPath

	params := buildGetSnapshotParams(gitalyAddress, buildPbRepo("default", "foo/bar.git"))
	resp, _, err := doSendDataRequest("/api/v4/projects/:id/snapshot", "git-snapshot", params)
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

func buildGetSnapshotParams(gitalyAddress string, repo *pb.Repository) string {
	msg := serializedMessage("GetSnapshotRequest", &pb.GetSnapshotRequest{Repository: repo})
	return buildGitalyRPCParams(gitalyAddress, msg)
}

type rpcArg struct {
	k string
	v interface{}
}

// Gitlab asks workhorse to perform some long-running RPCs for it by sending
// the RPC arguments (which are protobuf messages) in HTTP response headers.
// The messages are encoded to JSON objects using pbjson, The strings are then
// re-encoded to JSON strings using json. We must replicate this behaviour here
func buildGitalyRPCParams(gitalyAddress string, rpcArgs ...rpcArg) string {
	built := map[string]interface{}{
		"GitalyServer": map[string]string{
			"Address": gitalyAddress,
			"Token":   "",
		},
	}

	for _, arg := range rpcArgs {
		built[arg.k] = arg.v
	}

	b, err := json.Marshal(interface{}(built))
	if err != nil {
		panic(err)
	}

	return string(b)
}

func buildPbRepo(storageName, relativePath string) *pb.Repository {
	return &pb.Repository{
		StorageName:  storageName,
		RelativePath: relativePath,
	}
}

func serializedMessage(name string, arg proto.Message) rpcArg {
	m := &jsonpb.Marshaler{}
	str, err := m.MarshalToString(arg)
	if err != nil {
		panic(err)
	}

	return rpcArg{name, str}
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
