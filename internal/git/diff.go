package git

import (
	"fmt"
	"io"
	"net/http"

	log "github.com/sirupsen/logrus"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/gitaly"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/golang/protobuf/jsonpb"
)

type diff struct{ senddata.Prefix }
type diffParams struct {
	RepoPath       string
	ShaFrom        string
	ShaTo          string
	GitalyServer   gitaly.Server
	RawDiffRequest string
}

var SendDiff = &diff{"git-diff:"}

func (d *diff) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params diffParams
	if err := d.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendDiff: unpack sendData: %v", err))
		return
	}

	if params.GitalyServer.Address != "" {
		handleSendDiffWithGitaly(w, r, &params)
	} else {
		handleSendDiffLocally(w, r, &params)
	}
}

func handleSendDiffWithGitaly(w http.ResponseWriter, r *http.Request, params *diffParams) {
	request := &pb.RawDiffRequest{}
	if err := jsonpb.UnmarshalString(params.RawDiffRequest, request); err != nil {
		helper.Fail500(w, r, fmt.Errorf("diff.RawDiff: %v", err))
	}

	diffClient, err := gitaly.NewDiffClient(params.GitalyServer)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("diff.RawDiff: %v", err))
	}

	if err := diffClient.SendRawDiff(r.Context(), w, request); err != nil {
		helper.LogError(
			r,
			&copyError{fmt.Errorf("diff.RawDiff: request=%v, err=%v", request, err)},
		)
	}
}

func handleSendDiffLocally(w http.ResponseWriter, r *http.Request, params *diffParams) {
	log.WithFields(log.Fields{
		"shaFrom": params.ShaFrom,
		"shaTo":   params.ShaTo,
		"path":    r.URL.Path,
	}).Print("SendDiff: sending diff")

	gitDiffCmd := gitCommand("git", "--git-dir="+params.RepoPath, "diff", params.ShaFrom, params.ShaTo)
	stdout, err := gitDiffCmd.StdoutPipe()
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendDiff: create stdout pipe: %v", err))
		return
	}

	if err := gitDiffCmd.Start(); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendDiff: start %v: %v", gitDiffCmd.Args, err))
		return
	}
	defer helper.CleanUpProcessGroup(gitDiffCmd)

	w.Header().Del("Content-Length")
	if _, err := io.Copy(w, stdout); err != nil {
		helper.LogError(
			r,
			&copyError{fmt.Errorf("SendDiff: copy %v stdout: %v", gitDiffCmd.Args, err)},
		)
		return
	}
	if err := gitDiffCmd.Wait(); err != nil {
		helper.LogError(r, fmt.Errorf("SendDiff: wait for %v: %v", gitDiffCmd.Args, err))
		return
	}
}
