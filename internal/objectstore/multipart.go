package objectstore

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	log "github.com/sirupsen/logrus"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// ErrNotEnoughParts will be used when writing more than size * len(partURLs)
var ErrNotEnoughParts = errors.New("Not enough Parts")

// Multipart represents a MultipartUpload on a S3 compatible Object Store service.
// It can be used as io.WriteCloser for uploading an object
type Multipart struct {
	// CompleteURL is a presigned URL for CompleteMultipartUpload
	CompleteURL string
	// AbortURL is a presigned URL for AbortMultipartUpload
	AbortURL string
	// DeleteURL is a presigned URL for RemoveObject
	DeleteURL string

	// writeCloser is the writer bound to the UploadPart body
	writeCloser io.WriteCloser
	// uploadError is the last error occourred during upload
	uploadError error
	// ctx is the internal context bound to the upload request
	ctx context.Context

	// etags stores each Part ETag. This is a needed parameter for CompleteMultipartUpload
	etags []string
}

// CompleteMultipartUpload it the S3 CompleteMultipartUpload body
type CompleteMultipartUpload struct {
	Part []completeMultipartUploadPart
}

type completeMultipartUploadPart struct {
	PartNumber int
	ETag       string
}

// NewMultipart provides Multipart pointer that can be used for uploading. Data written will be split buffered on disk up to size bytes
// then uploaded with S3 Upload Part. Once Multipart is Closed a final call to CompleteMultipartUpload will be sent.
// In case of any error a call to AbortMultipartUpload will be made to cleanup all the resources
func NewMultipart(ctx context.Context, partURLs []string, completeURL, abortURL, deleteURL string, timeout time.Duration, size int64) (*Multipart, error) {
	started := time.Now()
	o := &Multipart{
		CompleteURL: completeURL,
		AbortURL:    abortURL,
		DeleteURL:   deleteURL,
	}

	pr, pw := io.Pipe()
	o.writeCloser = pw

	file, err := ioutil.TempFile("", "part-buffer")
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return nil, fmt.Errorf("Unable to create a temporary file for buffering: %v", err)
	}

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
		os.Remove(file.Name())

		if o.uploadError != nil {
			fmt.Println("-> Something went wrong. Aborting uploads")
			objectStorageUploadRequestsRequestFailed.Inc()
			o.abort()
		}

		// wait for provided context to finish before performing cleanup
		<-ctx.Done()
		o.delete()
	}()

	go func() {
		defer cancelFn()
		defer objectStorageUploadsOpen.Dec()
		defer pr.Close()

		fmt.Println("-> Multipart Main loop")

		for partNumber, partURL := range partURLs {
			fmt.Println("-> Waiting to receive part", partNumber+1)

			src := io.LimitReader(pr, size)
			file.Seek(0, io.SeekStart)
			n, err := io.Copy(file, src)
			if err != nil {
				o.uploadError = fmt.Errorf("Cannot write part %d to disk: %v", partNumber+1, err)
				return
			}
			if n == 0 {
				fmt.Println("-> Nothing to read")
				break
			}
			fmt.Println("-> Received", n, "bytes")

			file.Seek(0, io.SeekStart)
			fmt.Println("-> Uploading part", partNumber+1)
			etag, err := o.uploadPart(partURL, file, timeout, n)
			if err != nil {
				o.uploadError = fmt.Errorf("Cannot upload part %d: %v", partNumber+1, err)
				return
			}
			o.etags = append(o.etags, etag)
		}

		if n, _ := io.Copy(ioutil.Discard, pr); n > 0 {
			o.uploadError = ErrNotEnoughParts
			return
		}

		fmt.Println("-> Completing multipart")
		// Complete Multipart
		cmu := &CompleteMultipartUpload{}
		for n, etag := range o.etags {
			cmu.Part = append(cmu.Part, completeMultipartUploadPart{PartNumber: n + 1, ETag: etag})
		}
		body, err := xml.Marshal(cmu)
		if err != nil {
			o.uploadError = fmt.Errorf("Cannot marshal CompleteMultipartUpload request: %v", err)
			return
		}

		req, err := http.NewRequest("POST", o.CompleteURL, bytes.NewReader(body))
		if err != nil {
			o.uploadError = fmt.Errorf("Cannot create CompleteMultipartUpload request: %v", err)
			return
		}
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/xml")
		req = req.WithContext(o.ctx)

		resp, err := httpClient.Do(req)
		if err != nil {
			o.uploadError = fmt.Errorf("POST request %q: %v", helper.ScrubURLParams(o.CompleteURL), err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			o.uploadError = StatusCodeError(fmt.Errorf("POST request %v returned: %s", helper.ScrubURLParams(o.CompleteURL), resp.Status))
			return
		}
	}()

	return o, nil
}

func (m *Multipart) uploadPart(url string, body io.Reader, timeout time.Duration, size int64) (string, error) {
	part, err := newObject(m.ctx, url, "", timeout, size, false)
	if err != nil {
		return "", err
	}

	_, err = io.CopyN(part, body, size)
	if err != nil {
		return "", err
	}

	err = part.Close()
	if err != nil {
		return "", err
	}

	return part.MD5(), nil
}

// Write implements the standard io.Writer interface: it writes data to the UploadPart body.
func (m *Multipart) Write(p []byte) (int, error) {
	return m.writeCloser.Write(p)
}

// Close implements the standard io.Closer interface: it closes the http client request.
// This method will also wait for the connection to terminate and return any error occurred during the upload
func (m *Multipart) Close() error {
	if err := m.writeCloser.Close(); err != nil {
		return err
	}

	<-m.ctx.Done()

	return m.uploadError
}

func (m *Multipart) delete() {
	syncAndDelete(m.ctx, m.DeleteURL)
}

func (m *Multipart) abort() {
	syncAndDelete(m.ctx, m.AbortURL)
}

// syncAndDelete wait for sync Context to be Done and then performs the requested HTTP call
func syncAndDelete(sync context.Context, url string) {
	if url == "" {
		return
	}

	<-sync.Done()

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
