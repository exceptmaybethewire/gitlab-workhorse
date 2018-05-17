package filestore

import (
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

	//MultipartUpload parameters
	// PartSize is the exact size of each uploaded part. Only the last one can be smaller
	PartSize int64
	// PresignedParts contains the presigned URLs for each part
	PresignedParts []string
	// PresignedCompleteMultipart is a presigned URL for CompleteMulipartUpload
	PresignedCompleteMultipart string
	// PresignedAbortMultipart is a presigned URL for AbortMultipartUpload
	PresignedAbortMultipart string
}

// IsLocal checks if the options require the writing of the file on disk
func (s *SaveFileOpts) IsLocal() bool {
	return s.LocalTempPath != ""
}

// IsRemote checks if the options requires a remote upload
func (s *SaveFileOpts) IsRemote() bool {
	return s.PresignedPut != "" || s.IsMultipart()
}

// IsMultipart checks if the options requires a Multipart upload
func (s *SaveFileOpts) IsMultipart() bool {
	return s.PartSize > 0
}

// GetOpts converts GitLab api.Response to a proper SaveFileOpts
func GetOpts(apiResponse *api.Response) *SaveFileOpts {
	timeout := time.Duration(apiResponse.RemoteObject.Timeout) * time.Second
	if timeout == 0 {
		timeout = objectstore.DefaultObjectStoreTimeout
	}

	opts := SaveFileOpts{
		LocalTempPath:   apiResponse.TempPath,
		RemoteID:        apiResponse.RemoteObject.ID,
		RemoteURL:       apiResponse.RemoteObject.GetURL,
		PresignedPut:    apiResponse.RemoteObject.StoreURL,
		PresignedDelete: apiResponse.RemoteObject.DeleteURL,
		Timeout:         timeout,
	}

	if multiParams := apiResponse.RemoteObject.MultipartUpload; multiParams != nil {
		opts.PartSize = multiParams.PartSize
		opts.PresignedCompleteMultipart = multiParams.CompleteURL
		opts.PresignedAbortMultipart = multiParams.AbortURL
		opts.PresignedParts = make([]string, len(multiParams.PartURLs))
		copy(opts.PresignedParts, multiParams.PartURLs)
	}

	return &opts
}
