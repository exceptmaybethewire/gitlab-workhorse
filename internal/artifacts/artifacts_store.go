package artifacts

import (
	"context"
	"fmt"
	"mime/multipart"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"
)

func (a *artifactsUploadProcessor) storeFile(ctx context.Context, formName, fileName string, writer *multipart.Writer) error {
	if !a.opts.IsRemote() {
		return nil
	}

	if a.stored {
		return nil
	}

	fh, err := filestore.SaveFileFromDisk(ctx, fileName, a.opts)
	if err != nil {
		return fmt.Errorf("Uploading to object store failed. %s", err)
	}

	for field, value := range fh.GitLabFinalizeFields(formName) {
		writer.WriteField(field, value)
	}

	// Allow to upload only once using given credentials
	a.stored = true
	return nil
}
