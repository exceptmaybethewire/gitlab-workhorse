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

	uploader
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
func NewMultipart(ctx context.Context, partURLs []string, completeURL, abortURL, deleteURL string, deadline time.Time, size int64) (*Multipart, error) {
	file, err := ioutil.TempFile("", "part-buffer")
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return nil, fmt.Errorf("Unable to create a temporary file for buffering: %v", err)
	}

	pr, pw := io.Pipe()
	uploadCtx, cancelFn := context.WithDeadline(ctx, deadline)
	m := &Multipart{
		CompleteURL: completeURL,
		AbortURL:    abortURL,
		DeleteURL:   deleteURL,
		uploader:    newUploader(uploadCtx, pw),
	}

	go m.trackUploadTime()
	go m.cleanup(ctx, file)

	objectStorageUploadsOpen.Inc()

	go func() {
		defer cancelFn()
		defer objectStorageUploadsOpen.Dec()
		defer func() {
			// This will be returned as error to the next write operation on the pipe
			pr.CloseWithError(m.uploadError)
		}()

		fmt.Println("-> Multipart Main loop")

		cmu := &CompleteMultipartUpload{}
		for i, partURL := range partURLs {
			partNumber := i + 1
			fmt.Println("-> Waiting to receive part", partNumber)

			src := io.LimitReader(pr, size)
			_, err := file.Seek(0, io.SeekStart)
			if err != nil {
				m.uploadError = fmt.Errorf("Cannot rewind part %d temporary dump : %v", partNumber, err)
				return
			}

			n, err := io.Copy(file, src)
			if err != nil {
				m.uploadError = fmt.Errorf("Cannot write part %d to disk: %v", partNumber, err)
				return
			}
			if n == 0 {
				fmt.Println("-> Nothing to read")
				break
			}
			fmt.Println("-> Received", n, "bytes")

			_, err = file.Seek(0, io.SeekStart)
			if err != nil {
				m.uploadError = fmt.Errorf("Cannot rewind part %d temporary dump : %v", partNumber, err)
				return
			}

			fmt.Println("-> Uploading part", partNumber)
			etag, err := m.uploadPart(partURL, file, deadline, n)
			if err != nil {
				m.uploadError = fmt.Errorf("Cannot upload part %d: %v", partNumber, err)
				return
			}
			cmu.Part = append(cmu.Part, completeMultipartUploadPart{PartNumber: partNumber, ETag: etag})
		}

		if n, _ := io.Copy(ioutil.Discard, pr); n > 0 {
			m.uploadError = ErrNotEnoughParts
			return
		}

		if err := m.complete(cmu); err != nil {
			m.uploadError = err
			return
		}
	}()

	return m, nil
}

func (m *Multipart) trackUploadTime() {
	started := time.Now()
	<-m.ctx.Done()
	objectStorageUploadTime.Observe(time.Since(started).Seconds())
}

func (m *Multipart) cleanup(ctx context.Context, file *os.File) {
	// wait for the upload to finish
	<-m.ctx.Done()
	if err := os.Remove(file.Name()); err != nil {
		log.WithError(err).WithField("file", file.Name()).Warning("Unable to delete temporary file")
	}

	if m.uploadError != nil {
		fmt.Println("-> Something went wrong. Aborting uploads")
		objectStorageUploadRequestsRequestFailed.Inc()
		m.abort()
	}

	// We have now succesfully uploaded the file to object storage. Another
	// goroutine will hand off the object to gitlab-rails.
	<-ctx.Done()

	// gitlab-rails is now done with the object so it's time to delete it.
	m.delete()
}

func (m *Multipart) complete(cmu *CompleteMultipartUpload) error {
	fmt.Println("-> Completing multipart")
	body, err := xml.Marshal(cmu)
	if err != nil {
		return fmt.Errorf("Cannot marshal CompleteMultipartUpload request: %v", err)
	}

	req, err := http.NewRequest("POST", m.CompleteURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("Cannot create CompleteMultipartUpload request: %v", err)
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/xml")
	req = req.WithContext(m.ctx)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST request %q: %v", helper.ScrubURLParams(m.CompleteURL), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST request %v returned: %s", helper.ScrubURLParams(m.CompleteURL), resp.Status)
	}

	return nil
}

func (m *Multipart) uploadPart(url string, body io.Reader, deadline time.Time, size int64) (string, error) {
	part, err := newObject(m.ctx, url, "", deadline, size, false)
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

func (m *Multipart) delete() {
	m.syncAndDelete(m.DeleteURL)
}

func (m *Multipart) abort() {
	m.syncAndDelete(m.AbortURL)
}
