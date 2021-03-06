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

	uploader
}

// NewObject opens an HTTP connection to Object Store and returns an Object pointer that can be used for uploading.
func NewObject(ctx context.Context, putURL, deleteURL string, putHeaders map[string]string, deadline time.Time, size int64) (*Object, error) {
	return newObject(ctx, putURL, deleteURL, putHeaders, deadline, size, true)
}

func newObject(ctx context.Context, putURL, deleteURL string, putHeaders map[string]string, deadline time.Time, size int64, metrics bool) (*Object, error) {
	started := time.Now()
	pr, pw := io.Pipe()
	// we should prevent pr.Close() otherwise it may shadow error set with pr.CloseWithError(err)
	req, err := http.NewRequest(http.MethodPut, putURL, ioutil.NopCloser(pr))
	if err != nil {
		if metrics {
			objectStorageUploadRequestsRequestFailed.Inc()
		}
		return nil, fmt.Errorf("PUT %q: %v", helper.ScrubURLParams(putURL), err)
	}
	req.ContentLength = size

	for k, v := range putHeaders {
		req.Header.Set(k, v)
	}

	uploadCtx, cancelFn := context.WithDeadline(ctx, deadline)
	o := &Object{
		PutURL:    putURL,
		DeleteURL: deleteURL,
		uploader:  newMD5Uploader(uploadCtx, pw),
	}

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

		o.extractETag(resp.Header.Get("ETag"))
		if o.etag != o.md5Sum() {
			o.uploadError = fmt.Errorf("ETag mismatch. expected %q got %q", o.md5Sum(), o.etag)
			return
		}
	}()

	return o, nil
}

func (o *Object) delete() {
	o.syncAndDelete(o.DeleteURL)
}
