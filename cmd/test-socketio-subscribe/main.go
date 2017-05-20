package main

import (
	"log"
	"net/url"

	"time"

	"github.com/graarh/golang-socketio"
	"github.com/graarh/golang-socketio/transport"
)

type SubscripeEndpoint struct {
	ID       string
	Endpoint string
	Data     url.Values
}

type ReceivedData struct {
	Status int
	Data   interface{}
	Error  string
}

func main() {
	c, err := gosocketio.Dial(
		gosocketio.GetUrl("localhost", 8181, false),
		transport.GetDefaultWebsocketTransport(),
	)
	if err != nil {
		log.Fatalln(err)
	}
	err = c.On("test", func(c *gosocketio.Channel) {
		log.Println("HERE")
	})

	ep := SubscripeEndpoint{
		ID:       "value",
		Endpoint: "/gitlab-org/gitlab-test/pipelines.json",
	}
	log.Println("before ACK")
	result, err := c.Ack("subscribe:endpoint", ep, time.Minute)
	if err != nil {
		log.Fatalln(err)
	}
	log.Println("ACK:", result)

	err = c.On("update:value", func(c *gosocketio.Channel, data ReceivedData) {
		log.Println(data)
	})
	if err != nil {
		log.Fatalln(err)
	}

	//do something, handlers and functions are same as server ones
	time.Sleep(time.Hour)

	//close connection
	c.Close()
}
