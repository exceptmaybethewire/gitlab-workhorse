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

	err := handleGetInfoRefsWithGitaly(r.Context(), w, a, rpc, gitProtocol)

	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("handleGetInfoRefs: %v", err))
	}
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
