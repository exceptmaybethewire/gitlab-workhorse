package objectstore

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// httpTransport defines a http.Transport with values
// that are more restrictive than for http.DefaultTransport,
// they define shorter TLS Handshake, and more agressive connection closing
// to prevent the connection hanging and reduce FD usage
var httpTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 10 * time.Second,
	}).DialContext,
	MaxIdleConns:          2,
	IdleConnTimeout:       30 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 10 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}

var httpClient = &http.Client{
	Transport: httpTransport,
}

type StatusCodeError error

// Object represents an object on a S3 compatible Object Store service.
// It can be used as io.WriteCloser for uploading an object
type Object struct {
	// PutURL is a presigned URL for PutObject
	PutURL string
	// DeleteURL is a presigned URL for RemoveObject
	DeleteURL string
	// md5 is the checksum provided by the Object Store
	md5 string

	// writeCloser is the writer bound to the PutObject body
	writeCloser io.WriteCloser
	// uploadError is the last error occourred during upload
	uploadError error
	// ctx is the internal context bound to the upload request
	ctx context.Context
}

// NewObject opens an HTTP connection to Object Store and returns an Object pointer that can be used for uploading.
func NewObject(ctx context.Context, putURL, deleteURL string, deadline time.Time, size int64) (*Object, error) {
	return newObject(ctx, putURL, deleteURL, deadline, size, true)
}

func newObject(ctx context.Context, putURL, deleteURL string, deadline time.Time, size int64, metrics bool) (*Object, error) {
	started := time.Now()
	o := &Object{
		PutURL:    putURL,
		DeleteURL: deleteURL,
	}

	pr, pw := io.Pipe()
	o.writeCloser = pw

	// we should prevent pr.Close() otherwise it may shadow error set with pr.CloseWithError(err)
	req, err := http.NewRequest(http.MethodPut, o.PutURL, ioutil.NopCloser(pr))
	if err != nil {
		if metrics {
			objectStorageUploadRequestsRequestFailed.Inc()
		}
		return nil, fmt.Errorf("PUT %q: %v", helper.ScrubURLParams(o.PutURL), err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")

	uploadCtx, cancelFn := context.WithDeadline(ctx, deadline)
	o.ctx = uploadCtx

	if metrics {
		objectStorageUploadsOpen.Inc()
	}

	go func() {
		// wait for the upload to finish
		<-o.ctx.Done()
		if metrics {
			objectStorageUploadTime.Observe(time.Since(started).Seconds())
		}

		// wait for provided context to finish before performing cleanup
		<-ctx.Done()
		o.delete()
	}()

	go func() {
		defer cancelFn()
		if metrics {
			defer objectStorageUploadsOpen.Dec()
		}
		defer func() {
			// This will be returned as error to the next write operation on the pipe
			pr.CloseWithError(o.uploadError)
		}()

		req = req.WithContext(o.ctx)

		resp, err := httpClient.Do(req)
		if err != nil {
			if metrics {
				objectStorageUploadRequestsRequestFailed.Inc()
			}
			o.uploadError = fmt.Errorf("PUT request %q: %v", helper.ScrubURLParams(o.PutURL), err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			if metrics {
				objectStorageUploadRequestsInvalidStatus.Inc()
			}
			o.uploadError = StatusCodeError(fmt.Errorf("PUT request %v returned: %s", helper.ScrubURLParams(o.PutURL), resp.Status))
			return
		}

		o.extractMD5(resp.Header)
	}()

	return o, nil
}

// Write implements the standard io.Writer interface: it writes data to the PutObject body.
func (o *Object) Write(p []byte) (int, error) {
	return o.writeCloser.Write(p)
}

// Close implements the standard io.Closer interface: it closes the http client request.
// This method will also wait for the connection to terminate and return any error occurred during the upload
func (o *Object) Close() error {
	if err := o.writeCloser.Close(); err != nil {
		return err
	}

	<-o.ctx.Done()

	return o.uploadError
}

// MD5 returns the md5sum of the uploaded returned by the Object Store provider via ETag Header.
// This method will wait until upload context is done before returning.
func (o *Object) MD5() string {
	<-o.ctx.Done()

	return o.md5
}

func (o *Object) extractMD5(h http.Header) {
	etag := h.Get("ETag")
	if etag != "" && etag[0] == '"' {
		etag = etag[1 : len(etag)-1]
	}
	o.md5 = etag
}

func (o *Object) delete() {
	syncAndDelete(o.ctx, o.DeleteURL)
}
