package asyncprocessing

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"strings"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/redis"

	"net/http"
	"net/url"

	"github.com/googollee/go-socket.io"
)

type SubscripeEndpoint struct {
	Endpoint string
	Data     url.Values
}

func (s *SubscripeEndpoint) RedisKey() string {
	return "etag:" + s.Endpoint
}

type subscriptionData struct {
	SubscripeEndpoint

	ID       string
	socket   socketio.Socket
	task     interface{}
	lastEtag string
}

type subResponse struct {
	Status int
	Data   interface{}
	Error  string
}

func (s *subscriptionData) emit(response subResponse) {
	s.socket.Emit("update:"+s.ID, response)
}

func (s *subscriptionData) handle(status redis.WatchKeyStatus, err error) {
	log.Println("SUB HANDLE", s.ID, status, err)
	if err != nil {
		log.Println("Subscription error:", err)
		return
	}

	switch status {
	case redis.WatchKeyStatusStopped:
		return

	case redis.WatchKeyStatusNoChange, redis.WatchKeyStatusTimeout:
		s.Start()
		return

	case redis.WatchKeyStatusAlreadyChanged, redis.WatchKeyStatusSeenChange:
		defer s.Start()
		break
	}

	u, err := url.Parse(s.Endpoint)
	if err != nil {
		log.Println("Subscription error:", err)
		return
	}

	u.RawQuery = s.Data.Encode()

	req := &http.Request{
		Method: "GET",
		URL:    api.RebaseUrl(u, apiHandler.URL, ""),
		Header: helper.HeaderClone(s.socket.Request().Header),
	}

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		s.emit(subResponse{Error: err.Error()})
	} else {
		defer resp.Body.Close()

		if resp.StatusCode/100 == 2 {
			etag := resp.Header.Get("etag")
			if strings.HasPrefix(etag, `W/`) {
				etag = etag[2:]
			}
			etag = strings.Trim(etag, `"`)
			if etag != "" {
				s.lastEtag = etag
			}
		}

		r := subResponse{Status: resp.StatusCode}
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			r.Error = err.Error()
		} else {
			json.Unmarshal(data, &r.Data)
		}
		s.emit(r)
	}
}

func (s *subscriptionData) Start() error {
	log.Println("Start watching:", s.RedisKey(), "with last value:", s.lastEtag)
	s.task = redis.AsyncWatchKey(s.RedisKey(), s.lastEtag, time.Hour, s.handle)
	return nil
}

func (s *subscriptionData) Stop() {
	if s.task != nil {
		redis.StopAsyncWatchKey(s.task)
		s.task = nil
	}
}

type ConnectionSubscriptions map[string]*subscriptionData

var lock sync.RWMutex
var data = make(map[socketio.Socket]ConnectionSubscriptions)
var subId = 0

var apiHandler *api.API

func handleSubscribeEndpoint(so socketio.Socket, ep SubscripeEndpoint) (string, error) {
	lock.Lock()
	defer lock.Unlock()

	log.Println("SUB", ep)

	subs := data[so]
	if subs == nil {
		subs = make(ConnectionSubscriptions)
		data[so] = subs
	}

	subId++

	sub := new(subscriptionData)
	sub.ID = fmt.Sprintf("%d", subId)
	sub.SubscripeEndpoint = ep
	sub.socket = so

	if err := sub.Start(); err != nil {
		return "", err
	}

	subs[sub.ID] = sub
	return sub.ID, nil
}

func handleUnsubscribeEndpoint(so socketio.Socket, id string) error {
	lock.Lock()
	defer lock.Unlock()

	log.Println("UNSUB", id)

	subs := data[so]
	if subs == nil {
		return nil
	}

	sub := subs[id]
	if sub == nil {
		return nil
	}
	delete(subs, id)

	sub.Stop()
	return nil
}

func unsubscribeAll(so socketio.Socket) {
	lock.Lock()
	defer lock.Unlock()

	subs := data[so]
	if subs == nil {
		return
	}

	delete(data, so)

	for _, sub := range subs {
		sub.Stop()
	}
}

func handleConnection(so socketio.Socket) error {
	log.Println("New connection", so)

	so.Emit("test")

	err := so.On("subscribe", func(ep SubscripeEndpoint) string {
		log.Println("subscribe", ep)
		id, err := handleSubscribeEndpoint(so, ep)
		log.Println("subscribe ID", id, err)
		return id
	})
	if err != nil {
		return err
	}

	err = so.On("unsubscribe", func(id string) {
		log.Println("unsubscribe", id)
		handleUnsubscribeEndpoint(so, id)
	})
	if err != nil {
		return err
	}

	return nil
}

func Start(server *socketio.Server, api *api.API) error {
	apiHandler = api

	err := server.On("connection", handleConnection)
	if err != nil {
		return err
	}

	err = server.On("error", func(so socketio.Socket, err error) {
		log.Println("error:", err)
	})
	if err != nil {
		return err
	}
	return nil
}
