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
	dynamicBufferSize uint
	handler           http.Handler
}

func New(size uint, h http.Handler) http.Handler {
	return &requestBuffer{dynamicBufferSize: size, handler: h}
}

func (b *requestBuffer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := b.buffer(r.Body)
	if err != nil {
		helper.Fail500(w, fmt.Errorf("buffer.Requests: %v", err))
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

	peekBuffer, err := staticBuffer(body)
	if err != nil {
		return nil, err
	}
	if len(peekBuffer) == 0 {
		return &emptyReadCloser{}, nil
	}

	memoryBuffer, done, err := b.dynamicBufferWithPrefix(body, peekBuffer)
	if err != nil {
		return nil, err
	}
	if done {
		return ioutil.NopCloser(bytes.NewReader(memoryBuffer)), nil
	}

	return fileBufferWithPrefix(body, memoryBuffer)
}

func staticBuffer(body io.Reader) ([]byte, error) {
	peekArray := [1]byte{}
	peekBuffer := peekArray[:]
	peeked, err := body.Read(peekBuffer)
	if err != nil && err != io.EOF {
		return nil, err
	}

	return peekBuffer[:peeked], nil
}

func (b *requestBuffer) dynamicBufferWithPrefix(body io.Reader, prefix []byte) ([]byte, bool, error) {
	smallBuffer := make([]byte, b.dynamicBufferSize)
	for i := range prefix {
		smallBuffer[i] = prefix[i]
	}

	buffered := len(prefix)
	for {
		n, err := body.Read(smallBuffer[buffered:])
		buffered += n
		if err == io.EOF || buffered == len(smallBuffer) {
			break
		} else if err != nil {
			return nil, false, err
		}
	}

	return smallBuffer[:buffered], buffered < len(smallBuffer), nil
}

func fileBufferWithPrefix(body io.Reader, prefix []byte) (io.ReadCloser, error) {
	tempFile, err := ioutil.TempFile("", "gitlab-workhorse-request-body")
	if err != nil {
		return nil, err
	}

	if err := os.Remove(tempFile.Name()); err != nil {
		return nil, err
	}

	if n, err := io.Copy(tempFile, bytes.NewReader(prefix)); err != nil || n != int64(len(prefix)) {
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
