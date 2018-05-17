package filestore_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/objectstore/test"
)

func assertFileGetsRemovedAsync(t *testing.T, filePath string) {
	var err error

	// Poll because the file removal is async
	for i := 0; i < 100; i++ {
		_, err = os.Stat(filePath)
		if err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	assert.True(t, os.IsNotExist(err), "File hasn't been deleted during cleanup")
}

func assertObjectStoreDeletedAsync(t *testing.T, expectedDeletes int, osStub *test.ObjectstoreStub) {
	// Poll because the object removal is async
	for i := 0; i < 100; i++ {
		if osStub.DeletesCnt() == expectedDeletes {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.Equal(t, expectedDeletes, osStub.DeletesCnt(), "Object not deleted")
}

func TestSaveFileWrongSize(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmpFolder, err := ioutil.TempDir("", "workhorse-test-tmp")
	require.NoError(err)
	defer os.RemoveAll(tmpFolder)

	opts := &filestore.SaveFileOpts{LocalTempPath: tmpFolder, TempFilePrefix: "test-file"}
	fh, err := filestore.SaveFileFromReader(ctx, strings.NewReader(test.ObjectContent), test.ObjectSize+1, opts)
	assert.Error(err)
	_, isSizeError := err.(filestore.SizeError)
	assert.True(isSizeError, "Should fail with SizeError")
	assert.Nil(fh)
}

func TestSaveFromDiskNotExistingFile(t *testing.T) {
	assert := assert.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fh, err := filestore.SaveFileFromDisk(ctx, "/I/do/not/exist", &filestore.SaveFileOpts{})
	assert.Error(err, "SaveFileFromDisk should fail")
	assert.True(os.IsNotExist(err), "Provided file should not exists")
	assert.Nil(fh, "On error FileHandler should be nil")
}

func TestSaveFileWrongMD5(t *testing.T) {
	assert := assert.New(t)

	osStub, ts := test.StartObjectStoreWithCustomMD5(map[string]string{test.ObjectPath: "brokenMD5"})
	defer ts.Close()

	objectURL := ts.URL + test.ObjectPath

	opts := &filestore.SaveFileOpts{
		RemoteID:        "test-file",
		RemoteURL:       objectURL,
		PresignedPut:    objectURL + "?Signature=ASignature",
		PresignedDelete: objectURL + "?Signature=AnotherSignature",
	}

	ctx, cancel := context.WithCancel(context.Background())
	fh, err := filestore.SaveFileFromReader(ctx, strings.NewReader(test.ObjectContent), test.ObjectSize, opts)
	assert.Nil(fh)
	assert.Error(err)
	_, isMD5Error := err.(filestore.MD5Error)
	assert.True(isMD5Error, "Should fail with MD5Error")
	assert.Equal(1, osStub.PutsCnt(), "File not uploaded")

	cancel() // this will trigger an async cleanup
	assertObjectStoreDeletedAsync(t, 1, osStub)
}

func TestSaveFileFromDiskToLocalPath(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	f, err := ioutil.TempFile("", "workhorse-test")
	require.NoError(err)
	defer os.Remove(f.Name())

	_, err = fmt.Fprint(f, test.ObjectContent)
	require.NoError(err)

	tmpFolder, err := ioutil.TempDir("", "workhorse-test-tmp")
	require.NoError(err)
	defer os.RemoveAll(tmpFolder)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := &filestore.SaveFileOpts{LocalTempPath: tmpFolder}
	fh, err := filestore.SaveFileFromDisk(ctx, f.Name(), opts)
	assert.NoError(err)
	require.NotNil(fh)

	assert.NotEmpty(fh.LocalPath, "File not persisted on disk")
	_, err = os.Stat(fh.LocalPath)
	assert.NoError(err)
}

func TestSaveFile(t *testing.T) {
	type remote int
	const (
		remoteSingle remote = iota
		remoteMultipart
	)

	tmpFolder, err := ioutil.TempDir("", "workhorse-test-tmp")
	require.NoError(t, err)
	defer os.RemoveAll(tmpFolder)

	tests := []struct {
		name   string
		local  bool
		remote remote
	}{
		{name: "Local only", local: true},
		{name: "Remote Single only", remote: remoteSingle},
		{name: "Remote Single and Local", local: true, remote: remoteSingle},
		{name: "Remote Multipart only", remote: remoteMultipart},
		{name: "Remote Multipart and Local", local: true, remote: remoteMultipart},
	}

	for _, spec := range tests {
		t.Run(spec.name, func(t *testing.T) {
			assert := assert.New(t)

			var opts filestore.SaveFileOpts
			var expectedDeletes, expectedPuts int

			osStub, ts := test.StartObjectStore()
			defer ts.Close()

			switch spec.remote {
			case remoteSingle:
				objectURL := ts.URL + test.ObjectPath

				opts.RemoteID = "test-file"
				opts.RemoteURL = objectURL
				opts.PresignedPut = objectURL + "?Signature=ASignature"
				opts.PresignedDelete = objectURL + "?Signature=AnotherSignature"

				expectedDeletes = 1
				expectedPuts = 1
			case remoteMultipart:
				objectURL := ts.URL + test.ObjectPath

				opts.RemoteID = "test-file"
				opts.RemoteURL = objectURL
				opts.PresignedDelete = objectURL + "?Signature=AnotherSignature"
				opts.PartSize = int64(len(test.ObjectContent)/2) + 1
				opts.PresignedParts = []string{objectURL + "?partNumber=1", objectURL + "?partNumber=2"}
				opts.PresignedCompleteMultipart = objectURL + "?Signature=CompleteSignature"

				osStub.InitiateMultipartUpload(test.ObjectPath)
				expectedDeletes = 1
				expectedPuts = 2
			}

			if spec.local {
				opts.LocalTempPath = tmpFolder
				opts.TempFilePrefix = "test-file"
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			fh, err := filestore.SaveFileFromReader(ctx, strings.NewReader(test.ObjectContent), test.ObjectSize, &opts)
			assert.NoError(err)
			require.NotNil(t, fh)

			assert.Equal(opts.RemoteID, fh.RemoteID)
			assert.Equal(opts.RemoteURL, fh.RemoteURL)

			if spec.local {
				assert.NotEmpty(fh.LocalPath, "File not persisted on disk")
				_, err := os.Stat(fh.LocalPath)
				assert.NoError(err)

				dir := path.Dir(fh.LocalPath)
				assert.Equal(opts.LocalTempPath, dir)
				filename := path.Base(fh.LocalPath)
				beginsWithPrefix := strings.HasPrefix(filename, opts.TempFilePrefix)
				assert.True(beginsWithPrefix, fmt.Sprintf("LocalPath filename %q do not begin with TempFilePrefix %q", filename, opts.TempFilePrefix))
			} else {
				assert.Empty(fh.LocalPath, "LocalPath must be empty for non local uploads")
			}

			assert.Equal(test.ObjectSize, fh.Size)
			assert.Equal(test.ObjectMD5, fh.MD5())
			assert.Equal(test.ObjectSHA256, fh.SHA256())

			assert.Equal(expectedPuts, osStub.PutsCnt(), "ObjectStore PutObject count mismatch")
			assert.Equal(0, osStub.DeletesCnt(), "File deleted too early")

			cancel() // this will trigger an async cleanup
			assertObjectStoreDeletedAsync(t, expectedDeletes, osStub)
			assertFileGetsRemovedAsync(t, fh.LocalPath)

			// checking generated fields
			fields := fh.GitLabFinalizeFields("file")

			assert.Equal(fh.Name, fields["file.name"])
			assert.Equal(fh.LocalPath, fields["file.path"])
			assert.Equal(fh.RemoteURL, fields["file.remote_url"])
			assert.Equal(fh.RemoteID, fields["file.remote_id"])
			assert.Equal(strconv.FormatInt(test.ObjectSize, 10), fields["file.size"])
			assert.Equal(test.ObjectMD5, fields["file.md5"])
			assert.Equal(test.ObjectSHA1, fields["file.sha1"])
			assert.Equal(test.ObjectSHA256, fields["file.sha256"])
			assert.Equal(test.ObjectSHA512, fields["file.sha512"])
		})
	}
}
