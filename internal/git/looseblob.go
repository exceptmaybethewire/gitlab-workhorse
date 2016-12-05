package git

import (
	"bufio"
	"compress/zlib"
	"fmt"
	"io"
	"io/ioutil"
	"log"
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

func newBlobWriter(blobPath string, size int64) (*gitBlobWriter, error) {
	blobDir := path.Dir(blobPath)
	b := &gitBlobWriter{blobPath: blobPath}

	if err := b.mkdir(); err != nil {
		return nil, fmt.Errorf("newBlobWriter: create blob directory: %v", err)
	}

	tempFile, err := ioutil.TempFile(blobDir, "gitlab-cat-file")
	if err != nil {
		return nil, fmt.Errorf("newBlobWriter: create tempfile: %v", err)
	}

	b.file = tempFile
	b.zlibWriter = zlib.NewWriter(tempFile)

	if _, err := fmt.Fprintf(b.zlibWriter, "blob %d\x00", size); err != nil {
		b.Close()
		return nil, fmt.Errorf("newBlobWriter: write loose object header: %v", err)
	}

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
		return fmt.Errorf("gitBlobWriter: close zlib writer: %v", err)
	}

	if err := b.file.Close(); err != nil {
		return fmt.Errorf("gitBlobWriter: close tempfile: %v", err)
	}

	if err := os.Link(b.file.Name(), b.blobPath); err != nil && !os.IsExist(err) {
		return fmt.Errorf("gitBlobWriter: create loose object file: %v", err)
	}

	return nil
}

func (b *gitBlobWriter) mkdir() error {
	return os.MkdirAll(path.Dir(b.blobPath), 0755)
}

type looseBlobObject struct {
	objectPath string
	rawFile    *os.File
	zlibReader io.ReadCloser
}

func openLooseBlob(objectPath string) (*looseBlobObject, error) {
	var err error

	l := &looseBlobObject{objectPath: objectPath}
	if l.rawFile, err = os.Open(l.objectPath); err != nil {
		return nil, err
	}

	if l.zlibReader, err = zlib.NewReader(l.rawFile); err != nil {
		l.rawFile.Close()
		return nil, err
	}

	return l, nil
}

func (l *looseBlobObject) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("looseBlobOject: sending %q for %q", l.objectPath, r.URL.Path)

	bufReader := bufio.NewReader(l.zlibReader)
	objectHeader, err := bufReader.ReadString(0)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("looseBlobOject: read blob header: %v", err))
		return
	}

	objectHeader = strings.TrimSuffix(objectHeader, "\x00")
	prefix := "blob "
	if !strings.HasPrefix(objectHeader, prefix) {
		helper.LogError(r, fmt.Errorf("looseBlobOject: invalid object header: %q", objectHeader))
		return
	}
	setContentLength(w, strings.TrimPrefix(objectHeader, prefix))

	if _, err := io.Copy(w, bufReader); err != nil {
		helper.LogError(r, fmt.Errorf("looseBlobOject: copy loose blob: %v", err))
		return
	}
}

func (l *looseBlobObject) Close() error {
	if l.zlibReader != nil {
		l.zlibReader.Close()
	}

	if l.rawFile != nil {
		return l.rawFile.Close()
	}

	return nil
}
