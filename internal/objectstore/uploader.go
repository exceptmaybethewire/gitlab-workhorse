package objectstore

import (
	"context"
	"io"
	"net/http"

	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// uploader is am io.WriteCloser that can be used as write end of the uploding pipe.
type uploader struct {
	// writeCloser is the writer bound to the request body
	io.WriteCloser

	// uploadError is the last error occourred during upload
	uploadError error
	// ctx is the internal context bound to the upload request
	ctx context.Context
}

func newUploader(ctx context.Context, w io.WriteCloser) uploader {
	return uploader{WriteCloser: w, ctx: ctx}
}

// Close implements the standard io.Closer interface: it closes the http client request.
// This method will also wait for the connection to terminate and return any error occurred during the upload
func (u *uploader) Close() error {
	if err := u.WriteCloser.Close(); err != nil {
		return err
	}

	<-u.ctx.Done()

	if err := u.ctx.Err(); err == context.DeadlineExceeded {
		return err
	}

	return u.uploadError
}

// syncAndDelete wait for Context to be Done and then performs the requested HTTP call
func (u *uploader) syncAndDelete(url string) {
	if url == "" {
		return
	}

	<-u.ctx.Done()

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		log.WithError(err).WithField("object", helper.ScrubURLParams(url)).Warning("Delete failed")
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.WithError(err).WithField("object", helper.ScrubURLParams(url)).Warning("Delete failed")
		return
	}
	resp.Body.Close()
}
