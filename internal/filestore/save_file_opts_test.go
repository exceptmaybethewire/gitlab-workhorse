package filestore_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/objectstore"
)

func TestSaveFileOptsLocalAndRemote(t *testing.T) {
	tests := []struct {
		name          string
		localTempPath string
		presignedPut  string
		isLocal       bool
		isRemote      bool
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			assert := assert.New(t)

			opts := filestore.SaveFileOpts{
				LocalTempPath: test.localTempPath,
				PresignedPut:  test.presignedPut,
			}

			assert.Equal(test.isLocal, opts.IsLocal(), "IsLocal() mismatch")
			assert.Equal(test.isRemote, opts.IsRemote(), "IsRemote() mismatch")

		})
	}
}

func TestGetOpts(t *testing.T) {
	assert := assert.New(t)
	apiResponse := &api.Response{
		TempPath: "/tmp",
		ObjectStore: api.RemoteObjectStore{
			Timeout:   10,
			ObjectID:  "id",
			GetURL:    "http://get",
			StoreURL:  "http://store",
			DeleteURL: "http://delete",
		},
	}

	opts := filestore.GetOpts(apiResponse)

	assert.Equal(apiResponse.TempPath, opts.LocalTempPath)
	assert.Equal(time.Duration(apiResponse.ObjectStore.Timeout)*time.Second, opts.Timeout)
	assert.Equal(apiResponse.ObjectStore.ObjectID, opts.RemoteID)
	assert.Equal(apiResponse.ObjectStore.GetURL, opts.RemoteURL)
	assert.Equal(apiResponse.ObjectStore.StoreURL, opts.PresignedPut)
	assert.Equal(apiResponse.ObjectStore.DeleteURL, opts.PresignedDelete)
}

func TestGetOptsDefaultTimeout(t *testing.T) {
	assert := assert.New(t)

	opts := filestore.GetOpts(&api.Response{})

	assert.Equal(objectstore.DefaultObjectStoreTimeout, opts.Timeout)
}
