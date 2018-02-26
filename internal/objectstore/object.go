package objectstore

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// DefaultObjectStoreTimeout is the timeout for ObjectStore PutObject api calls
const DefaultObjectStoreTimeout = 360 * time.Second

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

// IsGoogleCloudStorage checks if the provided URL is from Google Cloud Storage service
func IsGoogleCloudStorage(u *url.URL) bool {
	return strings.ToLower(u.Host) == "storage.googleapis.com"
}

type MissingContentLengthError error
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
func NewObject(ctx context.Context, putURL, deleteURL string, timeout time.Duration, size int64) (*Object, error) {
	started := time.Now()
	o := &Object{
		PutURL:    putURL,
		DeleteURL: deleteURL,
	}

	pr, pw := io.Pipe()
	o.writeCloser = pw

	req, err := http.NewRequest(http.MethodPut, o.PutURL, pr)
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return nil, fmt.Errorf("PUT %q: %v", helper.ScrubURLParams(o.PutURL), err)
	}
	if size == -1 {
		if !IsGoogleCloudStorage(req.URL) {
			objectStorageUploadRequestsRequestFailed.Inc()
			return nil, MissingContentLengthError(fmt.Errorf("Unknown Content-Length not allowed on %s", req.URL.Host))
		}
	} else {
		req.ContentLength = size
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	if timeout == 0 {
		timeout = DefaultObjectStoreTimeout
	}

	uploadCtx, cancelFn := context.WithTimeout(ctx, timeout)
	o.ctx = uploadCtx

	objectStorageUploadsOpen.Inc()

	go func() {
		// wait for the upload to finish
		<-o.ctx.Done()
		objectStorageUploadTime.Observe(time.Since(started).Seconds())

		// wait for provided context to finish before performing cleanup
		<-ctx.Done()
		o.delete()
	}()

	go func() {
		defer cancelFn()
		defer objectStorageUploadsOpen.Dec()
		defer pr.Close()

		req = req.WithContext(o.ctx)

		resp, err := httpClient.Do(req)
		if err != nil {
			objectStorageUploadRequestsRequestFailed.Inc()
			o.uploadError = fmt.Errorf("PUT request %q: %v", helper.ScrubURLParams(o.PutURL), err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			objectStorageUploadRequestsInvalidStatus.Inc()
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
	if o.DeleteURL == "" {
		return
	}

	<-o.ctx.Done()

	req, err := http.NewRequest(http.MethodDelete, o.DeleteURL, nil)
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return
	}
	resp.Body.Close()
}
