package git

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// Git subprocess helpers
func gitCommand(gl_id string, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	// Start the command in its own process group (nice for signalling)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Explicitly set the environment for the Git command
	cmd.Env = []string{
		fmt.Sprintf("HOME=%s", os.Getenv("HOME")),
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
		fmt.Sprintf("LD_LIBRARY_PATH=%s", os.Getenv("LD_LIBRARY_PATH")),
		fmt.Sprintf("GL_ID=%s", gl_id),
		fmt.Sprintf("GL_PROTOCOL=http"),
	}
	// If we don't do something with cmd.Stderr, Git errors will be lost
	cmd.Stderr = os.Stderr
	return cmd
}

func execGitCommand(w http.ResponseWriter, r *http.Request, cmd *exec.Cmd) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("execGitCommand: create stdout pipe: %v", err))
		return
	}

	if err := cmd.Start(); err != nil {
		helper.Fail500(w, r, fmt.Errorf("execGitCommand: start %v: %v", cmd.Args, err))
		return
	}
	defer helper.CleanUpProcessGroup(cmd)

	w.Header().Del("Content-Length")
	if _, err := io.Copy(w, stdout); err != nil {
		helper.LogError(r, &copyError{fmt.Errorf("execGitCommand: copy %v stdout: %v", cmd.Args, err)})
		return
	}

	if err := cmd.Wait(); err != nil {
		helper.LogError(r, fmt.Errorf("execGitCommand: wait for %v: %v", cmd.Args, err))
		return
	}
}
