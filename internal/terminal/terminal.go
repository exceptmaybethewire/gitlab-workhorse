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
	"io"
	"log"
	"net/http"
	"os/exec"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"

	"github.com/kr/pty"
	"golang.org/x/net/websocket"
)

func Handler(myAPI *api.API) http.Handler {
	return myAPI.PreAuthorizeHandler(func(w http.ResponseWriter, r *http.Request, a *api.Response) {
		// TODO: get namespace/pod/container from the API response
		handleFunc(w, r)
	}, "authorize")
}

// GET /shell handler
// Launches /bin/bash and starts serving it via the terminal
func handleFunc(w http.ResponseWriter, r *http.Request) {
	defer log.Printf("Websocket session closed for %v", r.RemoteAddr)

	// start the websocket session:
	websocket.Handler(wsHandler).ServeHTTP(w, r)
}

func wsHandler(ws *websocket.Conn) {
	// wrap the websocket into UTF-8 wrappers:
	wrapper := NewWebSockWrapper(ws, WebSocketTextMode)
	stdout := wrapper
	stderr := wrapper

	// this one is optional (solves some weird issues with vim running under shell)
	stdin := &InputWrapper{ws}

	// starts new command in a newly allocated terminal:
	// TODO: replace /bin/bash with:
	//		 kubectl exec -ti <pod> --container <container name> -- /bin/bash
	cmd := exec.Command("/bin/bash")

	tty, err := pty.Start(cmd)
	if err != nil {
		panic(err)
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
