package git

import (
	"fmt"
	"log"
	"net/http"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
)

type show struct{ senddata.Prefix }
type showParams struct {
	RepoPath string
	Sha      string
	Format   string
}

var SendCommit = &show{"git-show-commit:"}

func (s *show) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params showParams
	if err := s.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendCommit: unpack sendData: %v", err))
		return
	}

	log.Printf("SendCommit: sending commit %q for %q", params.Sha, r.URL.Path)

	format, err := format(params)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendCommit: %v", err))
		return
	}

	gitShowCmd := gitCommand("", "git", "--git-dir="+params.RepoPath, "show", "-p", format, params.Sha)
	execGitCommand(w, r, gitShowCmd)
}

func format(params showParams) (string, error) {
	switch params.Format {
	case "diff":
		// An empty format will only show the raw diff, nothing else
		return "--format=", nil
	case "email":
		return "--format=email", nil
	default:
		return "", fmt.Errorf("format: %q is unsupported", params.Format)
	}
}
