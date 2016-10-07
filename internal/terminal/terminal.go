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

	"github.com/kr/pty"
	"golang.org/x/net/websocket"
)

var Handler = http.HandlerFunc(handleFunc)

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
	defer tty.Close()

	// pipe to/fro websocket to the TTY:
	go func() {
		io.Copy(stdout, tty)
	}()
	go func() {
		io.Copy(stderr, tty)
	}()
	go func() {
		io.Copy(tty, stdin)
	}()

	// wait for the command to exit, then close the websocket
	cmd.Wait()
}
