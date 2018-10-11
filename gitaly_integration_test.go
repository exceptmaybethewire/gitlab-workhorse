// TODO: Tests in this file need access to a real Gitaly server to run.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/gitaly"
)

var (
	gitalyAddress string
)

func init() {
	gitalyAddress = os.Getenv("GITALY_ADDRESS")
}

func skipUnlessRealGitaly(t *testing.T) {
	t.Log(gitalyAddress)
	if gitalyAddress != "" {
		return
	}

	t.Skip(`Please set GITALY_ADDRESS="..." to run Gitaly integration tests`)
}

func realGitalyAuthResponse(apiResponse *api.Response) *api.Response {
	apiResponse.GitalyServer.Address = gitalyAddress

	return apiResponse
}

func realGitalyOkBody(t *testing.T) *api.Response {
	return realGitalyAuthResponse(gitOkBody(t))
}

func ensureGitalyRepository(t *testing.T, apiResponse *api.Response) error {
	namespace, err := gitaly.NewNamespaceClient(apiResponse.GitalyServer)
	if err != nil {
		return err
	}

	repository, err := gitaly.NewRepositoryClient(apiResponse.GitalyServer)
	if err != nil {
		return err
	}

	// A hack for badly-configured Gitaly servers with a non-existent shard root
	addNsReq := &pb.AddNamespaceRequest{
		StorageName: apiResponse.Repository.StorageName,
		Name:        ".",
	}
	_, err = namespace.AddNamespace(context.Background(), addNsReq)
	if err != nil {
		return err
	}

	// Remove the repository if it already exists, for consistency
	rmNsReq := &pb.RemoveNamespaceRequest{
		StorageName: apiResponse.Repository.StorageName,
		Name:        apiResponse.Repository.RelativePath,
	}
	_, err = namespace.RemoveNamespace(context.Background(), rmNsReq)
	if err != nil {
		return err
	}

	createReq := &pb.CreateRepositoryFromURLRequest{
		Repository: &apiResponse.Repository,
		Url:        "https://gitlab.com/gitlab-org/gitlab-test.git",
	}

	_, err = repository.CreateRepositoryFromURL(context.Background(), createReq)
	return err
}

func TestAllowedClone(t *testing.T) {
	skipUnlessRealGitaly(t)

	// Create the repository in the Gitaly server
	apiResponse := realGitalyOkBody(t)
	require.NoError(t, ensureGitalyRepository(t, apiResponse))

	// Prepare test server and backend
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Do the git clone
	require.NoError(t, os.RemoveAll(scratchDir))
	cloneCmd := exec.Command("git", "clone", fmt.Sprintf("%s/%s", ws.URL, testRepo), checkoutDir)
	runOrFail(t, cloneCmd)

	// We may have cloned an 'empty' repository, 'git log' will fail in it
	logCmd := exec.Command("git", "log", "-1", "--oneline")
	logCmd.Dir = checkoutDir
	runOrFail(t, logCmd)
}

func TestAllowedShallowClone(t *testing.T) {
	skipUnlessRealGitaly(t)

	// Create the repository in the Gitaly server
	apiResponse := realGitalyOkBody(t)
	require.NoError(t, ensureGitalyRepository(t, apiResponse))

	// Prepare test server and backend
	ts := testAuthServer(nil, 200, apiResponse)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Shallow git clone (depth 1)
	require.NoError(t, os.RemoveAll(scratchDir))
	cloneCmd := exec.Command("git", "clone", "--depth", "1", fmt.Sprintf("%s/%s", ws.URL, testRepo), checkoutDir)
	runOrFail(t, cloneCmd)

	// We may have cloned an 'empty' repository, 'git log' will fail in it
	logCmd := exec.Command("git", "log", "-1", "--oneline")
	logCmd.Dir = checkoutDir
	runOrFail(t, logCmd)
}

func TestAllowedPush(t *testing.T) {
	skipUnlessRealGitaly(t)

	// TODO: I need to give Gitaly an API endpoint for the hooks to succeed

	// Create the repository in the Gitaly server
	apiResponse := realGitalyOkBody(t)
	require.NoError(t, ensureGitalyRepository(t, apiResponse))

	// Prepare the test server and backend
	ts := testAuthServer(nil, 200, realGitalyOkBody(t))
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Perform the git push
	pushCmd := exec.Command("git", "push", fmt.Sprintf("%s/%s", ws.URL, testRepo), fmt.Sprintf("master:%s", newBranch()))
	pushCmd.Dir = checkoutDir
	runOrFail(t, pushCmd)
}
