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

	gitProtocol := r.Header.Get("Git-Protocol")

	return handleUploadPackWithGitaly(r.Context(), a, buffer, w, gitProtocol)
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
