package git

import (
	"fmt"
	"io"
	"log"
	"net/http"
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

	log.Printf("SendBlob: sending %q for %q", params.BlobId, r.URL.Path)

	sizeOutput, err := gitCommand("", "git", "--git-dir="+params.RepoPath, "cat-file", "-s", params.BlobId).Output()
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: get blob size: %v", err))
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

	w.Header().Set("Content-Length", strings.TrimSpace(string(sizeOutput)))
	if _, err := io.Copy(w, stdout); err != nil {
		helper.LogError(r, &copyError{fmt.Errorf("SendBlob: copy git cat-file stdout: %v", err)})
		return
	}
	if err := gitShowCmd.Wait(); err != nil {
		helper.LogError(r, fmt.Errorf("SendBlob: wait for git cat-file: %v", err))
		return
	}
}
