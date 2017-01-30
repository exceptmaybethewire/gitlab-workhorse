package redis

import (
	"log"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
)

// KeyWatcher holds the information required for a single watch request
var keyWatcher map[string][]chan interface{}
var keyMutex sync.Mutex

// KeyChan holds a key and a channel
type KeyChan struct {
	Key  string
	Chan chan interface{}
}

func redisWorker() {
	log.Print("redisWorker running")

	conn := Get()
	defer conn.Close()

	psc := redis.PubSubConn{Conn: conn}
	if err := psc.PSubscribe("__keyevent:*__:set"); err != nil {
		return
	}
	defer psc.PUnsubscribe("__keyevent:*__:set")
	if err := psc.PSubscribe("__keyevent:*__:expired"); err != nil {
		return
	}
	defer psc.PUnsubscribe("__keyevent:*__:expired")

	for {
		switch v := psc.Receive().(type) {
		case redis.PMessage:
			notifyChanWatcher(string(v.Data))
		case error:
		}
	}
}

func notifyChanWatcher(key string) {
	keyMutex.Lock()
	if chanList, ok := keyWatcher[key]; ok {
		for _, c := range chanList {
			c <- true
		}
		delete(keyWatcher, key)
	}
	keyMutex.Unlock()
}

func addKeyChan(kc KeyChan) {
	keyMutex.Lock()
	defer keyMutex.Unlock()
	keyWatcher[kc.Key] = append(keyWatcher[kc.Key], kc.Chan)
}

// WaitKey waits for a key to be updated or expired
func WaitKey(key, value string) bool {
	kw := KeyChan{
		Key:  key,
		Chan: make(chan interface{}, 1),
	}

	addKeyChan(kw)

	val, _ := GetString(key)
	if val != value {
		return true // as mentioned, we don't care about the channels...
	}

	select {
	case <-kw.Chan:
		return true

	case <-time.After(time.Minute):
		return false
	}
}
