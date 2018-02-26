package git

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/gitaly"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

type blob struct{ senddata.Prefix }
type blobParams struct {
	RepoPath       string
	BlobId         string
	GitalyServer   gitaly.Server
	GetBlobRequest pb.GetBlobRequest
}

var SendBlob = &blob{"git-blob:"}

func (b *blob) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params blobParams
	if err := b.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: unpack sendData: %v", err))
		return
	}

	if params.GitalyServer.Address != "" {
		handleSendBlobWithGitaly(w, r, &params)
	} else {
		handleSendBlobLocally(w, r, &params)
	}
}

func handleSendBlobWithGitaly(w http.ResponseWriter, r *http.Request, params *blobParams) {
	blobClient, err := gitaly.NewBlobClient(params.GitalyServer)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("blob.GetBlob: %v", err))
	}

	if err := blobClient.SendBlob(r.Context(), w, &params.GetBlobRequest); err != nil {
		helper.Fail500(w, r, fmt.Errorf("blob.GetBlob: %v", err))
	}
}

func handleSendBlobLocally(w http.ResponseWriter, r *http.Request, params *blobParams) {
	log.Printf("SendBlob: sending %q for %q", params.BlobId, r.URL.Path)

	sizeOutput, err := gitCommand("git", "--git-dir="+params.RepoPath, "cat-file", "-s", params.BlobId).Output()
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendBlob: get blob size: %v", err))
		return
	}

	gitShowCmd := gitCommand("git", "--git-dir="+params.RepoPath, "cat-file", "blob", params.BlobId)
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
