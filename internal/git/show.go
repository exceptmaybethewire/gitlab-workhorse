package git

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
)

type show struct{ senddata.Prefix }
type showParams struct {
	RepoPath string
	Sha      string
}

var SendCommit = &show{"git-show-commit:"}

func (s *show) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params showParams
	if err := s.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendCommit: unpack sendData: %v", err))
		return
	}

	log.Printf("SendCommit: sending commit %q for %q", params.Sha, r.URL.Path)

	gitShowCmd := gitCommand("", "git", "--git-dir="+params.RepoPath, "show", "-p", "--format=", params.Sha, "--stdout")

	stdout, err := gitShowCmd.StdoutPipe()
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendCommit: create stdout pipe: %v", err))
		return
	}

	if err := gitShowCmd.Start(); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendCommit: start %v: %v", gitShowCmd.Args, err))
		return
	}
	defer helper.CleanUpProcessGroup(gitShowCmd)

	w.Header().Del("Content-Length")
	if _, err := io.Copy(w, stdout); err != nil {
		helper.LogError(r, &copyError{fmt.Errorf("SendCommit: copy %v stdout: %v", gitShowCmd.Args, err)})
		return
	}
	if err := gitShowCmd.Wait(); err != nil {
		helper.LogError(r, fmt.Errorf("SendCommit: wait for %v: %v", gitShowCmd.Args, err))
		return
	}
}
