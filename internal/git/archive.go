/*
In this file we handle 'git archive' downloads
*/

package git

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
)

type archive struct{ senddata.Prefix }
type archiveParams struct {
	RepoPath      string
	ArchivePath   string
	ArchivePrefix string
	CommitId      string
}

var SendArchive = &archive{"git-archive:"}

func (a *archive) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params archiveParams
	if err := a.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendArchive: unpack sendData: %v", err))
		return
	}

	var format string
	urlPath := r.URL.Path
	format, ok := parseBasename(filepath.Base(urlPath))
	if !ok {
		helper.Fail500(w, r, fmt.Errorf("SendArchive: invalid format: %s", urlPath))
		return
	}

	archiveFilename := path.Base(params.ArchivePath)

	if cachedArchive, err := os.Open(params.ArchivePath); err == nil {
		defer cachedArchive.Close()
		log.Printf("Serving cached file %q", params.ArchivePath)
		setArchiveHeaders(w, format, archiveFilename)
		// Even if somebody deleted the cachedArchive from disk since we opened
		// the file, Unix file semantics guarantee we can still read from the
		// open file in this process.
		http.ServeContent(w, r, "", time.Unix(0, 0), cachedArchive)
		return
	}

	// We assume the tempFile has a unique name so that concurrent requests are
	// safe. We create the tempfile in the same directory as the final cached
	// archive we want to create so that we can use an atomic link(2) operation
	// to finalize the cached archive.
	tempFile, err := prepareArchiveTempfile(path.Dir(params.ArchivePath), archiveFilename)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendArchive: create tempfile: %v", err))
		return
	}
	defer tempFile.Close()
	defer os.Remove(tempFile.Name())

	compressCmd, archiveFormat := parseArchiveFormat(format)

	archiveCmd := gitCommand("", "git", "--git-dir="+params.RepoPath, "archive", "--format="+archiveFormat, "--prefix="+params.ArchivePrefix+"/", params.CommitId)
	archiveStdout, err := archiveCmd.StdoutPipe()
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendArchive: archive stdout: %v", err))
		return
	}
	defer archiveStdout.Close()
	if err := archiveCmd.Start(); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendArchive: start %v: %v", archiveCmd.Args, err))
		return
	}
	defer helper.CleanUpProcessGroup(archiveCmd) // Ensure brute force subprocess clean-up

	var stdout io.ReadCloser
	if compressCmd == nil {
		stdout = archiveStdout
	} else {
		compressCmd.Stdin = archiveStdout
		compressCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		stdout, err = compressCmd.StdoutPipe()
		if err != nil {
			helper.Fail500(w, r, fmt.Errorf("SendArchive: compress stdout: %v", err))
			return
		}
		defer stdout.Close()

		if err := compressCmd.Start(); err != nil {
			helper.Fail500(w, r, fmt.Errorf("SendArchive: start %v: %v", compressCmd.Args, err))
			return
		}
		defer helper.CleanUpProcessGroup(compressCmd)

		archiveStdout.Close()
	}
	// Every Read() from stdout will be synchronously written to tempFile
	// before it comes out the TeeReader.
	archiveReader := io.TeeReader(stdout, tempFile)

	// Start writing the response
	setArchiveHeaders(w, format, archiveFilename)
	w.WriteHeader(200) // Don't bother with HTTP 500 from this point on, just return
	if _, err := io.Copy(w, archiveReader); err != nil {
		helper.LogError(r, &copyError{fmt.Errorf("SendArchive: copy 'git archive' output: %v", err)})
		return
	}
	if err := archiveCmd.Wait(); err != nil {
		helper.LogError(r, fmt.Errorf("SendArchive: archiveCmd: %v", err))
		return
	}
	if compressCmd != nil {
		if err := compressCmd.Wait(); err != nil {
			helper.LogError(r, fmt.Errorf("SendArchive: compressCmd: %v", err))
			return
		}
	}

	if err := finalizeCachedArchive(tempFile, params.ArchivePath); err != nil {
		helper.LogError(r, fmt.Errorf("SendArchive: finalize cached archive: %v", err))
		return
	}
}

func setArchiveHeaders(w http.ResponseWriter, format string, archiveFilename string) {
	w.Header().Del("Content-Length")
	w.Header().Add("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, archiveFilename))
	if format == "zip" {
		w.Header().Add("Content-Type", "application/zip")
	} else {
		w.Header().Add("Content-Type", "application/octet-stream")
	}
	w.Header().Add("Content-Transfer-Encoding", "binary")
	w.Header().Add("Cache-Control", "private")
}

func parseArchiveFormat(format string) (*exec.Cmd, string) {
	switch format {
	case "tar":
		return nil, "tar"
	case "tar.gz":
		return exec.Command("gzip", "-c", "-n"), "tar"
	case "tar.bz2":
		return exec.Command("bzip2", "-c"), "tar"
	case "zip":
		return nil, "zip"
	}
	return nil, "unknown"
}

func prepareArchiveTempfile(dir string, prefix string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return ioutil.TempFile(dir, prefix)
}

func finalizeCachedArchive(tempFile *os.File, archivePath string) error {
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Link(tempFile.Name(), archivePath); err != nil && !os.IsExist(err) {
		return err
	}

	return nil
}

func parseBasename(basename string) (string, bool) {
	var format string

	switch basename {
	case "archive.zip":
		format = "zip"
	case "archive.tar":
		format = "tar"
	case "archive", "archive.tar.gz", "archive.tgz", "archive.gz":
		format = "tar.gz"
	case "archive.tar.bz2", "archive.tbz", "archive.tbz2", "archive.tb2", "archive.bz2":
		format = "tar.bz2"
	default:
		return "", false
	}

	return format, true
}
