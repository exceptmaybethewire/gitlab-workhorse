package requestbuffer

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

type emptyReadCloser struct{}

func (_ *emptyReadCloser) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (_ *emptyReadCloser) Close() error {
	return nil
}

type requestBuffer struct {
	dynamicBufferSize int
	handler           http.Handler
}

func New(size int, h http.Handler) http.Handler {
	return &requestBuffer{dynamicBufferSize: size, handler: h}
}

func (b *requestBuffer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		b.handler.ServeHTTP(w, r)
		return
	}

	body, err := b.buffer(r.Body)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("requestBuffer.buffer: %v", err))
		return
	}
	defer body.Close()

	r.Body = body
	b.handler.ServeHTTP(w, r)
}

func (b *requestBuffer) buffer(body io.ReadCloser) (io.ReadCloser, error) {
	if body == nil {
		return &emptyReadCloser{}, nil
	}

	buffer := bytes.NewBuffer(make([]byte, b.dynamicBufferSize))
	_, err := io.Copy(buffer, io.LimitReader(body, int64(b.dynamicBufferSize)))
	if err != nil {
		return nil, err
	}

	if buffer.Len() < b.dynamicBufferSize {
		return ioutil.NopCloser(buffer), nil
	}

	return fileBufferWithPrefix(body, buffer)
}

func fileBufferWithPrefix(body io.Reader, prefix io.Reader) (io.ReadCloser, error) {
	tempFile, err := ioutil.TempFile("", "gitlab-workhorse-request-body")
	if err != nil {
		return nil, err
	}

	if err := os.Remove(tempFile.Name()); err != nil {
		return nil, err
	}

	if _, err := io.Copy(tempFile, prefix); err != nil {
		return nil, err
	}

	if _, err := io.Copy(tempFile, body); err != nil {
		return nil, err
	}

	if _, err := tempFile.Seek(0, 0); err != nil {
		return nil, err
	}

	return tempFile, nil
}
