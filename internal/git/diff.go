package git

import (
	"fmt"
	"log"
	"net/http"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
)

type diff struct{ senddata.Prefix }
type diffParams struct {
	RepoPath string
	ShaFrom  string
	ShaTo    string
}

var SendDiff = &diff{"git-diff:"}

func (d *diff) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params diffParams
	if err := d.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendDiff: unpack sendData: %v", err))
		return
	}

	log.Printf("SendDiff: sending diff between %q and %q for %q", params.ShaFrom, params.ShaTo, r.URL.Path)

	gitDiffCmd := gitCommand("", "git", "--git-dir="+params.RepoPath, "diff", params.ShaFrom, params.ShaTo)
	execGitCommand(w, r, gitDiffCmd)
}
