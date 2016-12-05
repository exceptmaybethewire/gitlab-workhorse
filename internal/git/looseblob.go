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
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

var objectIdRegex = regexp.MustCompile(`\A[a-f0-9]{40}\z`)

type blobPath struct {
	repoPath string
	blobId   []byte
}

func (bp *blobPath) Path() string {
	return path.Join(bp.repoPath, "objects", string(bp.blobId[:2]), string(bp.blobId[2:]))
}

func (bp *blobPath) dir() string {
	return path.Dir(bp.Path())
}

func newBlobPath(repoPath, blobId string) (*blobPath, error) {
	if ok := objectIdRegex.MatchString(blobId); !ok {
		return nil, fmt.Errorf("invalid blobId %q", blobId)
	}
	return &blobPath{repoPath, []byte(blobId)}, nil
}

type gitBlobWriter struct {
	file       *os.File
	zlibWriter *zlib.Writer
	*blobPath
}

func newBlobWriter(repoPath, blobId string, size int64) (b *gitBlobWriter, err error) {
	blobPath, err := newBlobPath(repoPath, blobId)
	if err != nil {
		return nil, err
	}

	b = &gitBlobWriter{blobPath: blobPath}
	defer func() {
		if err != nil {
			b.Close()
		}
	}()

	if err := b.mkdir(); err != nil {
		return b, fmt.Errorf("newBlobWriter: create blob directory: %v", err)
	}

	if b.file, err = ioutil.TempFile(b.dir(), "gitlab-cat-file"); err != nil {
		return b, fmt.Errorf("newBlobWriter: create tempfile: %v", err)
	}

	b.zlibWriter = zlib.NewWriter(b.file)

	if _, err := fmt.Fprintf(b.zlibWriter, "blob %d\x00", size); err != nil {
		return b, fmt.Errorf("newBlobWriter: write loose object header: %v", err)
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

	if err := os.Link(b.file.Name(), b.Path()); err != nil && !os.IsExist(err) {
		return fmt.Errorf("gitBlobWriter: create loose object file: %v", err)
	}

	return nil
}

func (b *gitBlobWriter) mkdir() error {
	return os.MkdirAll(b.dir(), 0755)
}

type looseBlobObject struct {
	rawFile    *os.File
	zlibReader io.ReadCloser
	*blobPath
}

func openLooseBlob(repoPath, blobId string) (l *looseBlobObject, err error) {
	blobPath, err := newBlobPath(repoPath, blobId)
	if err != nil {
		return nil, err
	}

	l = &looseBlobObject{blobPath: blobPath}
	defer func() {
		if err != nil {
			l.Close()
		}
	}()

	if l.rawFile, err = os.Open(l.Path()); err != nil {
		return l, err
	}

	if l.zlibReader, err = zlib.NewReader(l.rawFile); err != nil {
		return l, err
	}

	return l, nil
}

func (l *looseBlobObject) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("looseBlobOject: sending %q for %q", l.Path(), r.URL.Path)

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
