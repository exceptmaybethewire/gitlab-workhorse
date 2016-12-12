package looseblob

import (
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"os"
)

const nShaBytes = 20

type Writer struct {
	blobSha    []byte
	file       *os.File
	zlibWriter *zlib.Writer
	size       int64
	written    int64
	hasher     hash.Hash
	writer     io.Writer
	*blobPath
}

func NewWriter(repoPath, blobId string, size int64) (b *Writer, err error) {
	blobPath, err := newBlobPath(repoPath, blobId)
	if err != nil {
		return nil, err
	}

	b = &Writer{blobPath: blobPath, blobSha: make([]byte, nShaBytes)}
	defer func() {
		if err != nil {
			b.Close()
		}
	}()

	if n, err := hex.Decode(b.blobSha, []byte(blobId)); n != nShaBytes || err != nil {
		return b, fmt.Errorf("newBlobWriter: error decoding blobId (%d bytes): %v", n, err)
	}

	if err := b.mkdir(); err != nil {
		return b, fmt.Errorf("newBlobWriter: create blob directory: %v", err)
	}

	if b.file, err = ioutil.TempFile(b.dir(), "gitlab-cat-file"); err != nil {
		return b, fmt.Errorf("newBlobWriter: create tempfile: %v", err)
	}

	b.zlibWriter = zlib.NewWriter(b.file)

	b.size = size
	b.hasher = sha1.New()
	b.writer = io.MultiWriter(b.zlibWriter, b.hasher)
	if _, err := fmt.Fprintf(b.writer, "blob %d\x00", size); err != nil {
		return b, fmt.Errorf("newBlobWriter: write loose object header: %v", err)
	}

	return b, nil
}

func (b *Writer) Write(data []byte) (n int, err error) {
	n, err = b.writer.Write(data)
	b.written += int64(n)
	return n, err
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
	if b.written != b.size {
		return fmt.Errorf("gitBlobWriter: expected %d bytes, got %d", b.size, b.written)
	}

	sum := b.hasher.Sum([]byte{})
	for i := range sum {
		if sum[i] != b.blobSha[i] {
			return fmt.Errorf("gitBlobWriter: SHA1 mismatch: expected %x, got %x", b.blobSha, sum)
		}
	}

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
