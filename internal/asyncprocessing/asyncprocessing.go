package asyncprocessing

import (
	"encoding/json"
	"errors"
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
	ID       string
	Endpoint string
	Data     url.Values
}

func (s *SubscripeEndpoint) RedisKey() string {
	return "etag:" + s.Endpoint
}

type subscriptionData struct {
	SubscripeEndpoint

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
	if s.task == nil {
		return errors.New("failed to find")
	}
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

	sub := subs[ep.ID]
	if sub != nil {
		return "", errors.New("is already defined")
	}

	sub = new(subscriptionData)
	sub.SubscripeEndpoint = ep
	sub.socket = so

	if err := sub.Start(); err != nil {
		return "", err
	}

	subs[ep.ID] = sub
	return ep.ID, nil
}

func handleUnsubscribeEndpoint(so socketio.Socket, ep SubscripeEndpoint) error {
	lock.Lock()
	defer lock.Unlock()

	log.Println("UNSUB", ep)

	subs := data[so]
	if subs == nil {
		return nil
	}

	sub := subs[ep.ID]
	if sub == nil {
		return nil
	}
	delete(subs, ep.ID)

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

	err := so.On("subscribe:endpoint", func(ep SubscripeEndpoint) string {
		log.Println("subscribe:endpoint", ep)
		id, _ := handleSubscribeEndpoint(so, ep)
		return id
	})
	if err != nil {
		return err
	}

	err = so.On("unsubscribe:endpoint", func(ep SubscripeEndpoint) {
		log.Println("unsubscribe:endpoint", ep)
		handleUnsubscribeEndpoint(so, ep)
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
