package git

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/gitaly"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

var (
	// Testing is only set during workhorse testing.
	Testing = false
)

func GetInfoRefsHandler(a *api.API) http.Handler {
	return repoPreAuthorizeHandler(a, handleGetInfoRefs)
}

func handleGetInfoRefs(rw http.ResponseWriter, r *http.Request, a *api.Response) {
	w := NewHttpResponseWriter(rw)
	// Log 0 bytes in because we ignore the request body (and there usually is none anyway).
	defer w.Log(r, 0)

	rpc := getService(r)
	if !(rpc == "git-upload-pack" || rpc == "git-receive-pack") {
		// The 'dumb' Git HTTP protocol is not supported
		http.Error(w, "Not Found", 404)
		return
	}

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", rpc))
	w.Header().Set("Cache-Control", "no-cache")

	gitProtocol := r.Header.Get("Git-Protocol")

	var err error
	if a.GitalyServer.Address == "" && Testing {
		err = handleGetInfoRefsLocalTesting(w, a, rpc)
	} else {
		err = handleGetInfoRefsWithGitaly(r.Context(), w, a, rpc, gitProtocol)
	}

	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("handleGetInfoRefs: %v", err))
	}
}

// This code is not used in production. It is left over from before
// Gitaly. We left it here to allow local workhorse tests to keep working
// until we are done migrating Git HTTP to Gitaly.
func handleGetInfoRefsLocalTesting(w http.ResponseWriter, a *api.Response, rpc string) error {
	if err := pktLine(w, fmt.Sprintf("# service=%s\n", rpc)); err != nil {
		return fmt.Errorf("pktLine: %v", err)
	}
	if err := pktFlush(w); err != nil {
		return fmt.Errorf("pktFlush: %v", err)
	}

	cmd, err := startGitCommand(a, nil, w, rpc, "--advertise-refs")
	if err != nil {
		return fmt.Errorf("startGitCommand: %v", err)
	}
	defer helper.CleanUpProcessGroup(cmd) // Ensure brute force subprocess clean-up

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wait for %v: %v", cmd.Args, err)
	}

	return nil
}

func handleGetInfoRefsWithGitaly(ctx context.Context, w http.ResponseWriter, a *api.Response, rpc string, gitProtocol string) error {
	smarthttp, err := gitaly.NewSmartHTTPClient(a.GitalyServer)
	if err != nil {
		return fmt.Errorf("GetInfoRefsHandler: %v", err)
	}

	infoRefsResponseReader, err := smarthttp.InfoRefsResponseReader(ctx, &a.Repository, rpc, gitConfigOptions(a), gitProtocol)
	if err != nil {
		return fmt.Errorf("GetInfoRefsHandler: %v", err)
	}

	if _, err = io.Copy(w, infoRefsResponseReader); err != nil {
		return fmt.Errorf("GetInfoRefsHandler: copy Gitaly response: %v", err)
	}

	return nil
}
