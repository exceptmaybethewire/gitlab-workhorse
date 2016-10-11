/*
Copyright 2015 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package terminal

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"

	"github.com/kr/pty"
	"golang.org/x/net/websocket"
)

type session struct {
	OpenshiftApp     string
	OpenshiftProject string
	OpenshiftServer  string
	OpenshiftToken   string
}

func Handler(myAPI *api.API) http.Handler {
	return myAPI.PreAuthorizeHandler(func(w http.ResponseWriter, r *http.Request, a *api.Response) {
		s := session{}
		for _, item := range []struct {
			name, value string
			dest        *string
		}{
			{"OpenshiftApp", a.OpenshiftApp, &s.OpenshiftApp},
			{"OpenshiftProject", a.OpenshiftProject, &s.OpenshiftProject},
			{"OpenshiftServer", a.OpenshiftServer, &s.OpenshiftServer},
			{"OpenshiftToken", a.OpenshiftToken, &s.OpenshiftToken},
		} {
			if item.value == "" {
				helper.Fail500(w, r, fmt.Errorf("%s missing from API response", item.name))
				return
			}
			*item.dest = item.value
		}

		(&s).handleFunc(w, r)
	}, "authorize")
}

// GET /shell handler
// Launches /bin/bash and starts serving it via the terminal
func (s *session) handleFunc(w http.ResponseWriter, r *http.Request) {
	defer log.Printf("Websocket session closed for %v", r.RemoteAddr)

	// start the websocket session:
	websocket.Handler(s.wsHandler).ServeHTTP(w, r)
}

func (s *session) wsHandler(ws *websocket.Conn) {
	pod, err := s.getPod()
	if err != nil {
		fmt.Fprint(ws, "error: container not found")
		helper.LogError(nil, fmt.Errorf("terminal wsHandler get pod: %v", err))
		return
	}

	// wrap the websocket into UTF-8 wrappers:
	wrapper := NewWebSockWrapper(ws, WebSocketTextMode)
	stdout := wrapper
	stderr := wrapper

	// this one is optional (solves some weird issues with vim running under shell)
	stdin := &InputWrapper{ws}

	// starts new command in a newly allocated terminal:
	// Try /bin/bash, fall back to /bin/sh. The container may not have Bash.
	shell := "[ -x /bin/bash ] && exec /bin/bash -l -i; exec /bin/sh"
	args := s.kubectl("exec", pod, "-t", "-i", "--", "/bin/sh", "-c", shell)
	cmd := exec.Command(args[0], args[1:]...)

	tty, err := pty.Start(cmd)
	if err != nil {
		fmt.Fprint(ws, "error: could not connect to container")
		helper.LogError(nil, fmt.Errorf("terminal: wsHandler: start kubectl exec: %v", err))
	}
	defer func() {
		if process := cmd.Process; process != nil {
			process.Kill()
		}
		if err := tty.Close(); err != nil {
			log.Printf("close pty: %v", err)
		}
	}()

	copyCh := make(chan error, 3)
	// pipe to/fro websocket to the TTY:
	go func() {
		_, err := io.Copy(stdout, tty)
		copyCh <- err
	}()
	go func() {
		_, err := io.Copy(stderr, tty)
		copyCh <- err
	}()
	go func() {
		_, err := io.Copy(tty, stdin)
		copyCh <- err
	}()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-copyCh:
		if err != nil {
			log.Printf("terminal: websocket copy: %v", err)
		}
	case err := <-waitCh:
		if err != nil {
			log.Printf("terminal: command wait: %v", err)
		}
	}
}

func (s *session) kubectl(args ...string) []string {
	// TODO this is INSECURE because it leaks the token via the process status line
	kubectlBase := []string{
		"kubectl",
		"--server=" + s.OpenshiftServer,
		"--token=" + s.OpenshiftToken,
		"-n" + s.OpenshiftProject,
	}
	return append(kubectlBase, args...)
}

func (s *session) getPod() (string, error) {
	args := s.kubectl("get", "pod", "-oname", "-lapp="+s.OpenshiftApp)
	output, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) == 0 {
		return "", errors.New("kubectl get output was empty")
	}

	return strings.TrimPrefix(lines[0], "pod/"), nil
}
