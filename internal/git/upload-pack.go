package git

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/gitaly"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// Will not return a non-nil error after the response body has been
// written to.
func handleUploadPack(w *HttpResponseWriter, r *http.Request, a *api.Response) error {
	// The body will consist almost entirely of 'have XXX' and 'want XXX'
	// lines; these are about 50 bytes long. With a limit of 10MB the client
	// can send over 200,000 have/want lines.
	buffer, err := helper.ReadAllTempfile(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		return fmt.Errorf("ReadAllTempfile: %v", err)
	}
	defer buffer.Close()
	r.Body.Close()

	action := getService(r)
	writePostRPCHeader(w, action)

	if Testing && a.GitalyServer.Address == "" {
		// This code path is no longer reachable in GitLab 10.0
		err = handleUploadPackLocally(a, r, buffer, w, action)
	} else {
		gitProtocol := r.Header.Get("Git-Protocol")

		err = handleUploadPackWithGitaly(r.Context(), a, buffer, w, gitProtocol)
	}

	return err
}

func handleUploadPackLocally(a *api.Response, r *http.Request, stdin *os.File, stdout io.Writer, action string) error {
	isShallowClone := scanDeepen(stdin)
	if _, err := stdin.Seek(0, 0); err != nil {
		return fmt.Errorf("seek tempfile: %v", err)
	}

	cmd, err := startGitCommand(a, stdin, stdout, action)
	if err != nil {
		return fmt.Errorf("startGitCommand: %v", err)
	}
	defer helper.CleanUpProcessGroup(cmd)

	if err := cmd.Wait(); err != nil && !(isExitError(err) && isShallowClone) {
		helper.LogError(r, fmt.Errorf("wait for %v: %v", cmd.Args, err))
		// Return nil because the response body has been written to already.
		return nil
	}

	return nil
}

func handleUploadPackWithGitaly(ctx context.Context, a *api.Response, clientRequest io.Reader, clientResponse io.Writer, gitProtocol string) error {
	smarthttp, err := gitaly.NewSmartHTTPClient(a.GitalyServer)
	if err != nil {
		return fmt.Errorf("smarthttp.UploadPack: %v", err)
	}

	if err := smarthttp.UploadPack(ctx, &a.Repository, clientRequest, clientResponse, gitConfigOptions(a), gitProtocol); err != nil {
		return fmt.Errorf("smarthttp.UploadPack: %v", err)
	}

	return nil
}
