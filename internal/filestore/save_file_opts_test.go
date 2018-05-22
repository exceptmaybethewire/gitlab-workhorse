package filestore_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"
)

func TestSaveFileOptsLocalAndRemote(t *testing.T) {
	tests := []struct {
		name          string
		localTempPath string
		presignedPut  string
		partSize      int64
		isLocal       bool
		isRemote      bool
		isMultipart   bool
	}{
		{
			name:          "Only LocalTempPath",
			localTempPath: "/tmp",
			isLocal:       true,
		},
		{
			name:          "Both paths",
			localTempPath: "/tmp",
			presignedPut:  "http://example.com",
			isLocal:       true,
			isRemote:      true,
		},
		{
			name: "No paths",
		},
		{
			name:         "Only remoteUrl",
			presignedPut: "http://example.com",
			isRemote:     true,
		},
		{
			name:        "Multipart",
			partSize:    10,
			isRemote:    true,
			isMultipart: true,
		},
		{
			name:          "Multipart and Local",
			partSize:      10,
			localTempPath: "/tmp",
			isRemote:      true,
			isMultipart:   true,
			isLocal:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			assert := assert.New(t)

			opts := filestore.SaveFileOpts{
				LocalTempPath: test.localTempPath,
				PresignedPut:  test.presignedPut,
				PartSize:      test.partSize,
			}

			assert.Equal(test.isLocal, opts.IsLocal(), "IsLocal() mismatch")
			assert.Equal(test.isRemote, opts.IsRemote(), "IsRemote() mismatch")
			assert.Equal(test.isMultipart, opts.IsMultipart(), "IsMultipart() mismatch")
		})
	}
}

func TestGetOpts(t *testing.T) {
	tests := []struct {
		name      string
		multipart *api.MultipartUploadParams
	}{
		{
			name: "Single upload",
		}, {
			name: "Multipart upload",
			multipart: &api.MultipartUploadParams{
				PartSize:    10,
				CompleteURL: "http://complete",
				AbortURL:    "http://abort",
				PartURLs:    []string{"http://part1", "http://part2"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			assert := assert.New(t)
			apiResponse := &api.Response{
				TempPath: "/tmp",
				RemoteObject: api.RemoteObject{
					Timeout:         10,
					ID:              "id",
					GetURL:          "http://get",
					StoreURL:        "http://store",
					DeleteURL:       "http://delete",
					MultipartUpload: test.multipart,
				},
			}
			deadline := time.Now().Add(time.Duration(apiResponse.RemoteObject.Timeout) * time.Second)
			opts := filestore.GetOpts(apiResponse)

			assert.Equal(apiResponse.TempPath, opts.LocalTempPath)
			assert.WithinDuration(deadline, opts.Deadline, 100*time.Millisecond)
			assert.Equal(apiResponse.RemoteObject.ID, opts.RemoteID)
			assert.Equal(apiResponse.RemoteObject.GetURL, opts.RemoteURL)
			assert.Equal(apiResponse.RemoteObject.StoreURL, opts.PresignedPut)
			assert.Equal(apiResponse.RemoteObject.DeleteURL, opts.PresignedDelete)
			if test.multipart == nil {
				assert.False(opts.IsMultipart())
				assert.Empty(opts.PresignedCompleteMultipart)
				assert.Empty(opts.PresignedAbortMultipart)
				assert.Zero(opts.PartSize)
				assert.Empty(opts.PresignedParts)
			} else {
				assert.True(opts.IsMultipart())
				assert.Equal(test.multipart.CompleteURL, opts.PresignedCompleteMultipart)
				assert.Equal(test.multipart.AbortURL, opts.PresignedAbortMultipart)
				assert.Equal(test.multipart.PartSize, opts.PartSize)
				assert.Equal(test.multipart.PartURLs, opts.PresignedParts)
			}
		})
	}
}

func TestGetOptsDefaultTimeout(t *testing.T) {
	assert := assert.New(t)

	deadline := time.Now().Add(filestore.DefaultObjectStoreTimeout)
	opts := filestore.GetOpts(&api.Response{})

	assert.WithinDuration(deadline, opts.Deadline, 100*time.Millisecond)
}
