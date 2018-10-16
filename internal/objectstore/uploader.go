package objectstore

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"hash"
	"io"
	"net/http"

	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// Upload represents an upload to an ObjectStorage provider
type Upload interface {
	io.WriteCloser
	ETag() string
}

// uploader is an io.WriteCloser that can be used as write end of the uploading pipe.
type uploader struct {
	// etag is the object storage provided checksum
	etag string

	// md5 is an optional hasher for calculating md5 on the fly
	md5 hash.Hash

	w io.Writer
	c io.Closer

	// uploadError is the last error occourred during upload
	uploadError error
	// ctx is the internal context bound to the upload request
	ctx context.Context
}

func newUploader(ctx context.Context, w io.WriteCloser) uploader {
	return uploader{w: w, c: w, ctx: ctx}
}

func newMD5Uploader(ctx context.Context, w io.WriteCloser) uploader {
	hasher := md5.New()
	mw := io.MultiWriter(w, hasher)
	return uploader{w: mw, c: w, md5: hasher, ctx: ctx}
}

// Close implements the standard io.Closer interface: it closes the http client request.
// This method will also wait for the connection to terminate and return any error occurred during the upload
func (u *uploader) Close() error {
	if err := u.c.Close(); err != nil {
		return err
	}

	<-u.ctx.Done()

	if err := u.ctx.Err(); err == context.DeadlineExceeded {
		return err
	}

	return u.uploadError
}

func (u *uploader) Write(p []byte) (int, error) {
	return u.w.Write(p)
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

	// here we are not using u.ctx because we must perform cleanup regardless of parent context
	resp, err := httpClient.Do(req)
	if err != nil {
		log.WithError(err).WithField("object", helper.ScrubURLParams(url)).Warning("Delete failed")
		return
	}
	resp.Body.Close()
}

func (u *uploader) extractETag(rawETag string) {
	if rawETag != "" && rawETag[0] == '"' {
		rawETag = rawETag[1 : len(rawETag)-1]
	}
	u.etag = rawETag
}

func (u *uploader) md5Sum() string {
	if u.md5 == nil {
		return ""
	}

	checksum := u.md5.Sum(nil)
	return hex.EncodeToString(checksum)
}

// ETag returns the checksum of the uploaded object returned by the ObjectStorage provider via ETag Header.
// This method will wait until upload context is done before returning.
func (u *uploader) ETag() string {
	<-u.ctx.Done()

	return u.etag
}
