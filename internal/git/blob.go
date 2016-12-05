package git

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
)

type blob struct{ senddata.Prefix }
type blobParams struct{ RepoPath, BlobId string }

var SendBlob = &blob{"git-blob:"}

func (b *blob) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params blobParams
	if err := b.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: unpack sendData: %v", err))
		return
	}

	blobIdSlice := []byte(params.BlobId)
	blobPath := path.Join(params.RepoPath, "objects", string(blobIdSlice[:2]), string(blobIdSlice[2:]))

	if looseBlobObject, err := openLooseBlob(blobPath); err == nil {
		defer looseBlobObject.Close()
		looseBlobObject.ServeHTTP(w, r)
		return
	}

	log.Printf("SendBlob: sending %q for %q", params.BlobId, r.URL.Path)

	sizeOutput, err := gitCommand("", "git", "--git-dir="+params.RepoPath, "cat-file", "-s", params.BlobId).Output()
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: get blob size: %v", err))
		return
	}
	sizeInt64, err := strconv.ParseInt(strings.TrimSpace(string(sizeOutput)), 10, 64)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: parse size: %v", err))
		return
	}

	gitShowCmd := gitCommand("", "git", "--git-dir="+params.RepoPath, "cat-file", "blob", params.BlobId)
	stdout, err := gitShowCmd.StdoutPipe()
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: git cat-file stdout: %v", err))
		return
	}
	if err := gitShowCmd.Start(); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: start %v: %v", gitShowCmd, err))
		return
	}
	defer helper.CleanUpProcessGroup(gitShowCmd)

	blobWriter, err := newBlobWriter(blobPath, sizeInt64)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: create gitBlobWriter: %v", err))
		return
	}
	defer blobWriter.Close()

	setContentLength(w, fmt.Sprintf("%d", sizeInt64))

	blobReader := io.TeeReader(stdout, blobWriter)
	n, err := io.Copy(w, blobReader)

	if err != nil {
		helper.LogError(r, &copyError{fmt.Errorf("SendBlob: copy git cat-file stdout: %v", err)})
		return
	}

	if n != sizeInt64 {
		helper.LogError(r, &copyError{fmt.Errorf("SendBlob: copy git cat-file stdout: wrote %d bytes, expected %d", n, sizeInt64)})
		return
	}

	if err := gitShowCmd.Wait(); err != nil {
		helper.LogError(r, fmt.Errorf("SendBlob: wait for git cat-file: %v", err))
		return
	}

	if err := blobWriter.Finalize(); err != nil {
		helper.LogError(r, fmt.Errorf("SendBlob: finalize cached blob: %v", err))
		return
	}
}

func setContentLength(w http.ResponseWriter, size string) {
	w.Header().Set("Content-Length", size)
}
