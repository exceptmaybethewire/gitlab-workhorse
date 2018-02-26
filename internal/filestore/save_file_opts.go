package filestore

import (
	"net/url"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/objectstore"
)

// SaveFileOpts represents all the options available for saving a file to object store
type SaveFileOpts struct {
	// TempFilePrefix is the prefix used to create temporary local file
	TempFilePrefix string
	// LocalTempPath is the directory where to write a local copy of the file
	LocalTempPath string
	// RemoteID is the remote ObjectID provided by GitLab
	RemoteID string
	// RemoteURL is the final URL of the file
	RemoteURL string
	// PresignedPut is a presigned S3 PutObject compatible URL
	PresignedPut string
	// PresignedDelete is a presigned S3 DeleteObject compatible URL.
	PresignedDelete string
	// Timeout it the S3 operation timeout. If 0, objectstore.DefaultObjectStoreTimeout will be used
	Timeout time.Duration
}

// IsLocal checks if the options require the writing of the file on disk
func (s *SaveFileOpts) IsLocal() bool {
	return s.LocalTempPath != ""
}

// IsRemote checks if the options requires a remote upload
func (s *SaveFileOpts) IsRemote() bool {
	return s.PresignedPut != ""
}

func (s *SaveFileOpts) isGoogleCloudStorage() bool {
	if !s.IsRemote() {
		return false
	}

	getURL, err := url.Parse(s.RemoteURL)
	if err != nil {
		return false
	}

	return objectstore.IsGoogleCloudStorage(getURL)
}

// GetOpts converts GitLab api.Response to a proper SaveFileOpts
func GetOpts(apiResponse *api.Response) *SaveFileOpts {
	timeout := time.Duration(apiResponse.ObjectStore.Timeout) * time.Second
	if timeout == 0 {
		timeout = objectstore.DefaultObjectStoreTimeout
	}

	return &SaveFileOpts{
		LocalTempPath:   apiResponse.TempPath,
		RemoteID:        apiResponse.ObjectStore.ObjectID,
		RemoteURL:       apiResponse.ObjectStore.GetURL,
		PresignedPut:    apiResponse.ObjectStore.StoreURL,
		PresignedDelete: apiResponse.ObjectStore.DeleteURL,
		Timeout:         timeout,
	}
}
