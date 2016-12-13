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

	b = &Writer{blobPath: blobPath, blobSha: make([]byte, sha1.Size)}
	defer func() {
		if err != nil {
			b.Close()
		}
	}()

	if n, err := hex.Decode(b.blobSha, []byte(blobId)); n != sha1.Size || err != nil {
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
	if b.file != nil {
		os.Remove(b.file.Name())
	}
	return b.close()
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

	// We cannot use Close() because it removes the file we still need.
	if err := b.close(); err != nil {
		return fmt.Errorf("gitBlobWriter: close: %v", err)
	}

	// We link _after_ closing to obtain the close-to-open consistency of NFS.
	if err := os.Link(b.file.Name(), b.Path()); err != nil && !os.IsExist(err) {
		return fmt.Errorf("gitBlobWriter: create loose object file: %v", err)
	}

	return nil
}

func (b *Writer) mkdir() error {
	return os.MkdirAll(b.dir(), 0755)
}

func (b *Writer) close() error {
	// All Closers will be closed in order regardless of errors. If any one
	// errors, some error is returned.
	closers := []io.Closer{b.zlibWriter, b.file}
	errs := make([]error, len(closers))

	for i, c := range closers {
		if c != nil {
			errs[i] = c.Close()
		}
	}

	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	return nil
}
