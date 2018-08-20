package terminal

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/log"
)

var (
	// See doc/terminal.md for documentation of this subprotocol
	subprotocols             = []string{"terminal.gitlab.com", "base64.terminal.gitlab.com"}
	upgrader                 = &websocket.Upgrader{Subprotocols: subprotocols}
	ReauthenticationInterval = 5 * time.Minute
	BrowserPingInterval      = 30 * time.Second
)

func Handler(myAPI *api.API) http.Handler {
	return myAPI.PreAuthorizeHandler(func(w http.ResponseWriter, r *http.Request, a *api.Response) {
		if err := a.Terminal.Validate(); err != nil {
			helper.Fail500(w, r, err)
			return
		}

		proxy := NewProxy(2) // two stoppers: auth checker, max time
		checker := NewAuthChecker(
			authCheckFunc(myAPI, r, "authorize"),
			a.Terminal,
			proxy.StopCh,
		)
		defer checker.Close()
		go checker.Loop(ReauthenticationInterval)
		go closeAfterMaxTime(proxy, a.Terminal.MaxSessionTime)

		ProxyTerminal(w, r, a.Terminal, proxy)
	}, "authorize")
}

func ProxyTerminal(w http.ResponseWriter, r *http.Request, terminal *api.TerminalSettings, proxy *Proxy) {
	server, err := connectToServer(terminal, r)
	if err != nil {
		helper.Fail500(w, r, err)
		log.WithError(r.Context(), err).Print("Terminal: connecting to server failed")
		return
	}
	defer server.UnderlyingConn().Close()
	serverAddr := server.UnderlyingConn().RemoteAddr().String()

	client, err := upgradeClient(w, r)
	if err != nil {
		log.WithError(r.Context(), err).Print("Terminal: upgrading client to websocket failed")
		return
	}

	// Regularly send ping messages to the browser to keep the websocket from
	// being timed out by intervening proxies.
	go pingLoop(client)

	defer client.UnderlyingConn().Close()
	clientAddr := getClientAddr(r) // We can't know the port with confidence

	logEntry := log.WithFields(r.Context(), log.Fields{
		"clientAddr": clientAddr,
		"serverAddr": serverAddr,
	})

	logEntry.Print("Terminal: started proxying")

	defer logEntry.Print("Terminal: finished proxying")

	if err := proxy.Serve(server, client, serverAddr, clientAddr); err != nil {
		logEntry.WithError(err).Print("Terminal: error proxying")
	}
}

// In the future, we might want to look at X-Client-Ip or X-Forwarded-For
func getClientAddr(r *http.Request) string {
	return r.RemoteAddr
}

func upgradeClient(w http.ResponseWriter, r *http.Request) (Connection, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}

	return Wrap(conn, conn.Subprotocol()), nil
}

func pingLoop(conn Connection) {
	for {
		time.Sleep(BrowserPingInterval)
		deadline := time.Now().Add(5 * time.Second)
		if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
			// Either the connection was already closed so no further pings are
			// needed, or this connection is now dead and no further pings can
			// be sent.
			break
		}
	}
}

func connectToServer(terminal *api.TerminalSettings, r *http.Request) (Connection, error) {
	terminal = terminal.Clone()

	helper.SetForwardedFor(&terminal.Header, r)

	conn, _, err := terminal.Dial()
	if err != nil {
		return nil, err
	}

	return Wrap(conn, conn.Subprotocol()), nil
}

func closeAfterMaxTime(proxy *Proxy, maxSessionTime int) {
	if maxSessionTime == 0 {
		return
	}

	<-time.After(time.Duration(maxSessionTime) * time.Second)
	proxy.StopCh <- fmt.Errorf(
		"connection closed: session time greater than maximum time allowed - %v seconds",
		maxSessionTime,
	)
}
