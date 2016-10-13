package artifacts

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/zipartifacts"
)

type entry struct{ senddata.Prefix }
type entryParams struct{ Archive, Entry string }

var SendEntry = &entry{"artifacts-entry:"}

// Artifacts downloader doesn't support ranges when downloading a single file
func (e *entry) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params entryParams
	if err := e.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendEntry: unpack sendData: %v", err))
		return
	}

	log.Printf("SendEntry: sending %q from %q for %q", params.Entry, params.Archive, r.URL.Path)

	if params.Archive == "" || params.Entry == "" {
		helper.Fail500(w, r, fmt.Errorf("SendEntry: Archive or Entry is empty"))
		return
	}

	err := unpackFileFromZip(params.Archive, params.Entry, w.Header(), w)

	if os.IsNotExist(err) {
		http.NotFound(w, r)
	} else if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendEntry: %v", err))
	}
}

func detectFileContentType(fileName string) string {
	contentType := mime.TypeByExtension(filepath.Ext(fileName))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return contentType
}

func unpackFileFromZip(archiveFileName, encodedFilename string, headers http.Header, output io.Writer) error {
	fileName, err := zipartifacts.DecodeFileEntry(encodedFilename)
	if err != nil {
		return err
	}

	catFile := exec.Command("gitlab-zip-cat", archiveFileName, encodedFilename)
	catFile.Stderr = os.Stderr
	catFile.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := catFile.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create gitlab-zip-cat stdout pipe: %v", err)
	}

	if err := catFile.Start(); err != nil {
		return fmt.Errorf("start %v: %v", catFile.Args, err)
	}
	defer helper.CleanUpProcessGroup(catFile)

	basename := filepath.Base(fileName)
	reader := bufio.NewReader(stdout)
	contentLength, err := reader.ReadString('\n')
	if err != nil {
		if catFileErr := waitCatFile(catFile); catFileErr != nil {
			return catFileErr
		}
		return fmt.Errorf("read content-length: %v", err)
	}
	contentLength = strings.TrimSuffix(contentLength, "\n")

	// Write http headers about the file
	headers.Set("Content-Length", contentLength)
	headers.Set("Content-Type", detectFileContentType(fileName))
	headers.Set("Content-Disposition", "attachment; filename=\""+escapeQuotes(basename)+"\"")
	// Copy file body to client
	if _, err := io.Copy(output, reader); err != nil {
		return fmt.Errorf("copy stdout of %v: %v", catFile.Args, err)
	}

	return waitCatFile(catFile)
}

func waitCatFile(cmd *exec.Cmd) error {
	err := cmd.Wait()
	if err == nil {
		return nil
	}

	if st, ok := helper.ExitStatus(err); ok && st == zipartifacts.StatusEntryNotFound {
		return os.ErrNotExist
	}
	return fmt.Errorf("wait for %v to finish: %v", cmd.Args, err)

}
