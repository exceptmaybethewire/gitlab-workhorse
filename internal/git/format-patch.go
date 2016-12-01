package git

import (
	"fmt"
	"log"
	"net/http"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
)

type patch struct{ senddata.Prefix }
type patchParams struct {
	RepoPath string
	ShaFrom  string
	ShaTo    string
}

var SendPatch = &patch{"git-format-patch:"}

func (p *patch) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params patchParams
	if err := p.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendPatch: unpack sendData: %v", err))
		return
	}

	log.Printf("SendPatch: sending patch between %q and %q for %q", params.ShaFrom, params.ShaTo, r.URL.Path)

	gitRange := fmt.Sprintf("%s..%s", params.ShaFrom, params.ShaTo)
	gitPatchCmd := gitCommand("", "git", "--git-dir="+params.RepoPath, "format-patch", gitRange, "--stdout")
	execGitCommand(w, r, gitPatchCmd)
}
