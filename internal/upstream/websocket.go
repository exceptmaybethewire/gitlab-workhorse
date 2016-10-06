package upstream

import (
	"io"

	"golang.org/x/net/websocket"
)

var websocketEchoHandler = websocket.Handler(echoServer)

func echoServer(ws *websocket.Conn) {
	io.Copy(ws, ws)
}
