package git

import (
	"bufio"
	"compress/zlib"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

type gitBlobWriter struct {
	file       *os.File
	zlibWriter *zlib.Writer
	blobPath   string
}

func newBlobWriter(blobPath string) (*gitBlobWriter, error) {
	blobDir := path.Dir(blobPath)
	b := &gitBlobWriter{blobPath: blobPath}

	if err := b.mkdir(); err != nil {
		return nil, fmt.Errorf("SendBlob: create blob directory: %v", err)
	}

	tempFile, err := ioutil.TempFile(blobDir, "gitlab-cat-file")
	if err != nil {
		return nil, fmt.Errorf("SendBlob: create tempfile: %v", err)
	}

	b.file = tempFile
	b.zlibWriter = zlib.NewWriter(tempFile)

	return b, nil
}

func (b *gitBlobWriter) Write(data []byte) (n int, err error) {
	return b.zlibWriter.Write(data)
}

func (b *gitBlobWriter) Close() error {
	if b.zlibWriter != nil {
		b.zlibWriter.Close()
	}

	if b.file != nil {
		os.Remove(b.file.Name())
		return b.file.Close()
	}

	return nil
}

func (b *gitBlobWriter) Finalize() error {
	if err := b.zlibWriter.Close(); err != nil {
		return fmt.Errorf("SendBlob: close zlib writer: %v", err)
	}

	if err := b.file.Close(); err != nil {
		return fmt.Errorf("SendBlob: close tempfile: %v", err)
	}

	if err := b.mkdir(); err != nil {
		return fmt.Errorf("SendBlob: create blob directory: %v", err)
	}

	if err := os.Link(b.file.Name(), b.blobPath); err != nil && !os.IsExist(err) {
		return fmt.Errorf("SendBlob: create loose object file: %v", err)
	}

	return nil
}

func (b *gitBlobWriter) mkdir() error {
	return os.MkdirAll(path.Dir(b.blobPath), 0755)
}

func serveLooseObject(w http.ResponseWriter, r *http.Request, f *os.File) {
	zlibReader, err := zlib.NewReader(f)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: open zlib reader: %v", err))
		return
	}
	defer zlibReader.Close()

	bufReader := bufio.NewReader(zlibReader)
	objectHeader, err := bufReader.ReadString(0)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: read blob header: %v", err))
		return
	}

	objectHeader = strings.TrimSuffix(objectHeader, "\x00")
	if !strings.HasPrefix(objectHeader, "blob ") {
		helper.LogError(r, fmt.Errorf("SendBlob: invalid object header: %q", objectHeader))
		return
	}
	setContentLength(w, strings.TrimPrefix(objectHeader, "blob "))

	if _, err := io.Copy(w, bufReader); err != nil {
		helper.LogError(r, fmt.Errorf("SendBlob: copy loose blob: %v", err))
		return
	}
}
