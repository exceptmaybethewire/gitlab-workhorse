package looseblob

import (
	"compress/zlib"
	"fmt"
	"io/ioutil"
	"os"
)

type Writer struct {
	file       *os.File
	zlibWriter *zlib.Writer
	*blobPath
}

func NewWriter(repoPath, blobId string, size int64) (b *Writer, err error) {
	blobPath, err := newBlobPath(repoPath, blobId)
	if err != nil {
		return nil, err
	}

	b = &Writer{blobPath: blobPath}
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

func (b *Writer) Write(data []byte) (n int, err error) {
	return b.zlibWriter.Write(data)
}

func (b *Writer) Close() error {
	if b.zlibWriter != nil {
		b.zlibWriter.Close()
	}

	if b.file != nil {
		os.Remove(b.file.Name())
		return b.file.Close()
	}

	return nil
}

func (b *Writer) Finalize() error {
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

func (b *Writer) mkdir() error {
	return os.MkdirAll(b.dir(), 0755)
}
