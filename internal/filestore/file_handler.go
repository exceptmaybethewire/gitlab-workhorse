package filestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/objectstore"
)

type SizeError error

// ErrEntityTooLarge means that the uploaded content is bigger then maximum allowed size
var ErrEntityTooLarge = errors.New("Entity is too large")

// FileHandler represent a file that has been processed for upload
// it may be either uploaded to an ObjectStore and/or saved on local path.
type FileHandler struct {
	// LocalPath is the path on the disk where file has been stored
	LocalPath string

	// RemoteID is the objectID provided by GitLab Rails
	RemoteID string
	// RemoteURL is ObjectStore URL provided by GitLab Rails
	RemoteURL string

	// Size is the persisted file size
	Size int64

	// Name is the resource name to send back to GitLab rails.
	// It differ from the real file name in order to avoid file collisions
	Name string

	// a map containing different hashes
	hashes map[string]string
}

// SHA256 hash of the handled file
func (fh *FileHandler) SHA256() string {
	return fh.hashes["sha256"]
}

// MD5 hash of the handled file
func (fh *FileHandler) MD5() string {
	return fh.hashes["md5"]
}

// GitLabFinalizeFields returns a map with all the fields GitLab Rails needs in order to finalize the upload.
func (fh *FileHandler) GitLabFinalizeFields(prefix string) map[string]string {
	data := make(map[string]string)
	key := func(field string) string {
		if prefix == "" {
			return field
		}

		return fmt.Sprintf("%s.%s", prefix, field)
	}

	if fh.Name != "" {
		data[key("name")] = fh.Name
	}
	if fh.LocalPath != "" {
		data[key("path")] = fh.LocalPath
	}
	if fh.RemoteURL != "" {
		data[key("remote_url")] = fh.RemoteURL
	}
	if fh.RemoteID != "" {
		data[key("remote_id")] = fh.RemoteID
	}
	data[key("size")] = strconv.FormatInt(fh.Size, 10)
	for hashName, hash := range fh.hashes {
		data[key(hashName)] = hash
	}

	return data
}

// SaveFileFromReader persists the provided reader content to all the location specified in opts. A cleanup will be performed once ctx is Done
// Make sure the provided context will not expire before finalizing upload with GitLab Rails.
func SaveFileFromReader(ctx context.Context, reader io.Reader, size int64, opts *SaveFileOpts) (fh *FileHandler, err error) {
	var remoteWriter io.WriteCloser
	fh = &FileHandler{
		Name:      opts.TempFilePrefix,
		RemoteID:  opts.RemoteID,
		RemoteURL: opts.RemoteURL,
	}
	hashes := newMultiHash()
	writers := []io.Writer{hashes.Writer}
	defer func() {
		for _, w := range writers {
			if closer, ok := w.(io.WriteCloser); ok {
				closer.Close()
			}
		}
	}()

	if opts.IsMultipart() {
		remoteWriter, err = objectstore.NewMultipart(ctx, opts.PresignedParts, opts.PresignedCompleteMultipart, opts.PresignedAbortMultipart, opts.PresignedDelete, opts.Deadline, opts.PartSize)
		if err != nil {
			return nil, err
		}

		writers = append(writers, remoteWriter)
	} else if opts.IsRemote() {
		remoteWriter, err = objectstore.NewObject(ctx, opts.PresignedPut, opts.PresignedDelete, opts.Deadline, size)
		if err != nil {
			return nil, err
		}

		writers = append(writers, remoteWriter)
	}

	if opts.IsLocal() {
		fileWriter, err := fh.uploadLocalFile(ctx, opts)
		if err != nil {
			return nil, err
		}

		writers = append(writers, fileWriter)
	}

	if len(writers) == 1 {
		return nil, errors.New("Missing upload destination")
	}

	multiWriter := io.MultiWriter(writers...)
	fh.Size, err = io.Copy(multiWriter, reader)
	if err != nil {
		return nil, err
	}

	if size != -1 && size != fh.Size {
		return nil, SizeError(fmt.Errorf("Expected %d bytes but got only %d", size, fh.Size))
	}

	fh.hashes = hashes.finish()

	if opts.IsRemote() {
		// we need to close the writer in order to get ETag header
		err = remoteWriter.Close()
		if err != nil {
			if err == objectstore.ErrNotEnoughParts {
				return nil, ErrEntityTooLarge
			}
			return nil, err
		}
	}

	return fh, err
}

func (fh *FileHandler) uploadLocalFile(ctx context.Context, opts *SaveFileOpts) (io.WriteCloser, error) {
	// make sure TempFolder exists
	err := os.MkdirAll(opts.LocalTempPath, 0700)
	if err != nil {
		return nil, fmt.Errorf("uploadLocalFile: mkdir %q: %v", opts.LocalTempPath, err)
	}

	file, err := ioutil.TempFile(opts.LocalTempPath, opts.TempFilePrefix)
	if err != nil {
		return nil, fmt.Errorf("uploadLocalFile: create file: %v", err)
	}

	go func() {
		<-ctx.Done()
		os.Remove(file.Name())
	}()

	fh.LocalPath = file.Name()
	return file, nil
}

// SaveFileFromDisk open the local file fileName and calls SaveFileFromReader
func SaveFileFromDisk(ctx context.Context, fileName string, opts *SaveFileOpts) (fh *FileHandler, err error) {
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return nil, err
	}

	return SaveFileFromReader(ctx, file, fi.Size(), opts)
}
