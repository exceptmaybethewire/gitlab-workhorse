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

// Will not return a non-nil error after the response body has been
// written to.
func handleReceivePack(w *HttpResponseWriter, r *http.Request, a *api.Response) error {
	action := getService(r)
	writePostRPCHeader(w, action)

	cr, cw := helper.NewWriteAfterReader(r.Body, w)
	defer cw.Flush()

	var err error
	if a.GitalyServer.Address == "" {
		err = handleReceivePackLocally(a, r, cr, cw, action)
	} else {
		gitProtocol := r.Header.Get("Git-Protocol")

		err = handleReceivePackWithGitaly(r.Context(), a, cr, cw, gitProtocol)
	}

	return err
}

func handleReceivePackLocally(a *api.Response, r *http.Request, stdin io.Reader, stdout io.Writer, action string) error {
	cmd, err := startGitCommand(a, stdin, stdout, action)
	if err != nil {
		return fmt.Errorf("startGitCommand: %v", err)
	}
	defer helper.CleanUpProcessGroup(cmd)

	if err := cmd.Wait(); err != nil {
		helper.LogError(r, fmt.Errorf("wait for %v: %v", cmd.Args, err))
		// Return nil because the response body has been written to already.
		return nil
	}

	return nil
}

func handleReceivePackWithGitaly(ctx context.Context, a *api.Response, clientRequest io.Reader, clientResponse io.Writer, gitProtocol string) error {
	smarthttp, err := gitaly.NewSmartHTTPClient(a.GitalyServer)
	if err != nil {
		return fmt.Errorf("smarthttp.ReceivePack: %v", err)
	}

	if err := smarthttp.ReceivePack(ctx, &a.Repository, a.GL_ID, a.GL_USERNAME, a.GL_REPOSITORY, a.GitConfigOptions, clientRequest, clientResponse, gitProtocol); err != nil {
		return fmt.Errorf("smarthttp.ReceivePack: %v", err)
	}

	return nil
}
