package artifacts

import (
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"syscall"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/upload"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/zipartifacts"
)

type artifactsUploadProcessor struct {
	TempPath     string
	metadataFile string
}

func (a *artifactsUploadProcessor) ProcessFile(formName, fileName string, writer *multipart.Writer) error {
	//  ProcessFile for artifacts requires file form-data field name to eq `file`

	if formName != "file" {
		return fmt.Errorf("Invalid form field: %q", formName)
	}
	if a.metadataFile != "" {
		return fmt.Errorf("Artifacts request contains more than one file!")
	}

	// Create temporary file for metadata and store it's path
	tempFile, err := ioutil.TempFile(a.TempPath, "metadata_")
	if err != nil {
		return err
	}
	defer tempFile.Close()
	a.metadataFile = tempFile.Name()

	// Generate metadata and save to file
	zipMd := exec.Command("gitlab-zip-metadata", fileName)
	zipMd.Stderr = os.Stderr
	zipMd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	zipMd.Stdout = tempFile

	if err := zipMd.Start(); err != nil {
		return err
	}
	defer helper.CleanUpProcessGroup(zipMd)
	if err := zipMd.Wait(); err != nil {
		if st, ok := helper.ExitStatus(err); ok && st == zipartifacts.StatusNotZip {
			return nil
		}
		return err
	}

	// Pass metadata file path to Rails
	writer.WriteField("metadata.path", a.metadataFile)
	writer.WriteField("metadata.name", "metadata.gz")
	return nil
}

func (a *artifactsUploadProcessor) ProcessField(formName string, writer *multipart.Writer) error {
	return nil
}

func (a *artifactsUploadProcessor) Cleanup() {
	if a.metadataFile != "" {
		os.Remove(a.metadataFile)
	}
}

func UploadArtifacts(myAPI *api.API, h http.Handler) http.Handler {
	return myAPI.PreAuthorizeHandler(func(w http.ResponseWriter, r *http.Request, a *api.Response) {
		if a.TempPath == "" {
			helper.Fail500(w, r, fmt.Errorf("UploadArtifacts: TempPath is empty"))
			return
		}

		mg := &artifactsUploadProcessor{TempPath: a.TempPath}
		defer mg.Cleanup()

		upload.HandleFileUploads(w, r, h, a.TempPath, mg)
	}, "/authorize")
}
