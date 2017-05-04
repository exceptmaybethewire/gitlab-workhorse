package artifacts

import (
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/upload"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/zipartifacts"
)

type artifactsUploadProcessor struct {
	TempPath           string
	ObjectStore        api.RemoteObjectStore
	ObjectStoreTimeout time.Duration
	metadataFile       string
	stored             bool
}

func (a *artifactsUploadProcessor) generateMetadataFromZip(fileName string, metadataFile io.Writer) (bool, error) {
	// Generate metadata and save to file
	zipMd := exec.Command("gitlab-zip-metadata", fileName)
	zipMd.Stderr = os.Stderr
	zipMd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	zipMd.Stdout = metadataFile

	if err := zipMd.Start(); err != nil {
		return false, err
	}
	defer helper.CleanUpProcessGroup(zipMd)
	if err := zipMd.Wait(); err != nil {
		if st, ok := helper.ExitStatus(err); ok && st == zipartifacts.StatusNotZip {
			return false, nil
		}
		return false, err
	}

	return true, nil
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

	generatedMetadata, err := a.generateMetadataFromZip(fileName, tempFile)
	if err != nil {
		return fmt.Errorf("generateMetadataFromZip: %v", err)
	}

	if generatedMetadata {
		// Pass metadata file path to Rails
		writer.WriteField("metadata.path", a.metadataFile)
		writer.WriteField("metadata.name", "metadata.gz")
	}

	err = a.storeFile(formName, fileName, writer)
	if err != nil {
		return fmt.Errorf("storeFile: %v", err)
	}
	return nil
}

func (a *artifactsUploadProcessor) ProcessField(formName string, writer *multipart.Writer) error {
	return nil
}

func (a *artifactsUploadProcessor) Finalize() error {
	return nil
}

func (a *artifactsUploadProcessor) Name() string {
	return "artifacts"
}

func (a *artifactsUploadProcessor) Cleanup() {
	if a.metadataFile != "" {
		os.Remove(a.metadataFile)
	}
}

func UploadArtifacts(myAPI *api.API, h http.Handler, objectStoreTimeout time.Duration) http.Handler {
	return myAPI.PreAuthorizeHandler(func(w http.ResponseWriter, r *http.Request, a *api.Response) {
		if a.TempPath == "" {
			helper.Fail500(w, r, fmt.Errorf("UploadArtifacts: TempPath is empty"))
			return
		}

		mg := &artifactsUploadProcessor{
			TempPath:           a.TempPath,
			ObjectStore:        a.ObjectStore,
			ObjectStoreTimeout: objectStoreTimeout,
		}
		defer mg.Cleanup()

		upload.HandleFileUploads(w, r, h, a.TempPath, mg)
	}, "/authorize")
}
