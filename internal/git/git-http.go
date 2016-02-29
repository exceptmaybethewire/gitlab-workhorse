/*
In this file we handle the Git 'smart HTTP' protocol
*/

package git

import (
	"../api"
	"../helper"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func GetInfoRefs(a *api.API) http.Handler {
	return repoPreAuthorizeHandler(a, handleGetInfoRefs)
}

func PostRPC(a *api.API) http.Handler {
	return repoPreAuthorizeHandler(a, handlePostRPC)
}

func looksLikeRepo(p string) bool {
	// If /path/to/foo.git/objects exists then let's assume it is a valid Git
	// repository.
	if _, err := os.Stat(path.Join(p, "objects")); err != nil {
		log.Print(err)
		return false
	}
	return true
}

func repoPreAuthorizeHandler(myAPI *api.API, handleFunc api.HandleFunc) http.Handler {
	return myAPI.PreAuthorizeHandler(func(w http.ResponseWriter, r *http.Request, a *api.Response) {
		if a.RepoPath == "" {
			helper.Fail500(w, errors.New("repoPreAuthorizeHandler: RepoPath empty"))
			return
		}

		if !looksLikeRepo(a.RepoPath) {
			http.Error(w, "Not Found", 404)
			return
		}

		handleFunc(w, r, a)
	}, "")
}

func handleGetInfoRefs(w http.ResponseWriter, r *http.Request, a *api.Response) {
	rpc := r.URL.Query().Get("service")
	if !(rpc == "git-upload-pack" || rpc == "git-receive-pack") {
		// The 'dumb' Git HTTP protocol is not supported
		http.Error(w, "Not Found", 404)
		return
	}

	// Prepare our Git subprocess
	cmd := gitCommand(a.GL_ID, "git", subCommand(rpc), "--stateless-rpc", "--advertise-refs", a.RepoPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		helper.Fail500(w, fmt.Errorf("handleGetInfoRefs: stdout: %v", err))
		return
	}
	defer stdout.Close()
	if err := cmd.Start(); err != nil {
		helper.Fail500(w, fmt.Errorf("handleGetInfoRefs: start %v: %v", cmd.Args, err))
		return
	}
	defer helper.CleanUpProcessGroup(cmd) // Ensure brute force subprocess clean-up

	// Start writing the response
	w.Header().Add("Content-Type", fmt.Sprintf("application/x-%s-advertisement", rpc))
	w.Header().Add("Cache-Control", "no-cache")
	w.WriteHeader(200) // Don't bother with HTTP 500 from this point on, just return
	if err := pktLine(w, fmt.Sprintf("# service=%s\n", rpc)); err != nil {
		helper.LogError(fmt.Errorf("handleGetInfoRefs: pktLine: %v", err))
		return
	}
	if err := pktFlush(w); err != nil {
		helper.LogError(fmt.Errorf("handleGetInfoRefs: pktFlush: %v", err))
		return
	}
	if _, err := io.Copy(w, stdout); err != nil {
		helper.LogError(fmt.Errorf("handleGetInfoRefs: copy output of %v: %v", cmd.Args, err))
		return
	}
	if err := cmd.Wait(); err != nil {
		helper.LogError(fmt.Errorf("handleGetInfoRefs: wait for %v: %v", cmd.Args, err))
		return
	}
}

func handlePostRPC(w http.ResponseWriter, r *http.Request, a *api.Response) {
	var err error

	// Get Git action from URL
	action := filepath.Base(r.URL.Path)
	if !(action == "git-upload-pack" || action == "git-receive-pack") {
		// The 'dumb' Git HTTP protocol is not supported
		helper.Fail500(w, fmt.Errorf("handlePostRPC: unsupported action: %s", r.URL.Path))
		return
	}

	// Prepare our Git subprocess
	cmd := gitCommand(a.GL_ID, "git", subCommand(action), "--stateless-rpc", a.RepoPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		helper.Fail500(w, fmt.Errorf("handlePostRPC: stdout: %v", err))
		return
	}
	defer stdout.Close()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		helper.Fail500(w, fmt.Errorf("handlePostRPC: stdin: %v", err))
		return
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		helper.Fail500(w, fmt.Errorf("handlePostRPC: start %v: %v", cmd.Args, err))
		return
	}
	defer helper.CleanUpProcessGroup(cmd) // Ensure brute force subprocess clean-up

	// Write the client request body to Git's standard input
	if _, err := io.Copy(stdin, r.Body); err != nil {
		helper.Fail500(w, fmt.Errorf("handlePostRPC write to %v: %v", cmd.Args, err))
		return
	}
	// Signal to the Git subprocess that no more data is coming
	stdin.Close()

	// It may take a while before we return and the deferred closes happen
	// so let's free up some resources already.
	r.Body.Close()

	bodyWriter := w
	if action == "git-receive-pack" {
		// A 'git push' from the client has a small response so it should be OK
		// to buffer it in memory. If a Git hook on the server rejects the push
		// it is nice to return a non-200 HTTP status code to the HTTP Git client
		// so it 'knows' the push was not succesful. Because there is no way (?)
		// to distinguish between errors (e.g. disk full) and Git hook rejections
		// the error response from gitlab-workhorse will always be HTTP 500
		// (Internal Server Error).
		bodyWriter := &bytes.Buffer{} // buffer the entire response in memory
		defer flushBuffer(w, bodyWriter)
	}

	// Start writing the response
	w.Header().Add("Content-Type", fmt.Sprintf("application/x-%s-result", action))
	w.Header().Add("Cache-Control", "no-cache")

	// This io.Copy may take a long time, both for Git push and pull.
	if _, err := io.Copy(bodyWriter, stdout); err != nil {
		helper.Fail500(w, fmt.Errorf("handlePostRPC copy output of %v: %v", cmd.Args, err))
		return
	}
	if err := cmd.Wait(); err != nil {
		helper.Fail500(w, fmt.Errorf("handlePostRPC wait for %v: %v", cmd.Args, err))
		return
	}
}

func subCommand(rpc string) string {
	return strings.TrimPrefix(rpc, "git-")
}

func pktLine(w io.Writer, s string) error {
	_, err := fmt.Fprintf(w, "%04x%s", len(s)+4, s)
	return err
}

func pktFlush(w io.Writer) error {
	_, err := fmt.Fprint(w, "0000")
	return err
}

func flushBuffer(w http.ResponseWriter, buf io.Reader) {
	if _, err := io.Copy(w, buf); err != nil {
		helper.Fail500(w, fmt.Errorf("handlePostRPC flush response buffer: %v", err))
	}
}
