package redis

import (
	"log"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
)

// KeyWatcher holds the information required for a single watch request
var keyWatcher map[string][]chan bool
var keyMutex sync.Mutex

const (
	keyPubEventSet     = "__keyevent@*__:set"
	keyPubEventExpired = "__keyevent@*__:expired"
)

// KeyChan holds a key and a channel
type KeyChan struct {
	Key  string
	Chan chan bool
}

func redisWorker(wg *sync.WaitGroup) {
	keyWatcher = make(map[string][]chan bool)
	wg.Done()

	log.Print("redisWorker running")

	conn := Get()
	defer conn.Close()

	psc := redis.PubSubConn{Conn: conn}
	if err := psc.PSubscribe(keyPubEventSet); err != nil {
		return
	}
	defer psc.PUnsubscribe(keyPubEventSet)
	if err := psc.PSubscribe(keyPubEventExpired); err != nil {
		return
	}
	defer psc.PUnsubscribe(keyPubEventExpired)

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
	defer keyMutex.Unlock()
	if chanList, ok := keyWatcher[key]; ok {
		for _, c := range chanList {
			c <- true
		}
		delete(keyWatcher, key)
	}
}

func addKeyChan(kc *KeyChan) {
	keyMutex.Lock()
	defer keyMutex.Unlock()
	keyWatcher[kc.Key] = append(keyWatcher[kc.Key], kc.Chan)
}

func delKeyChan(kc *KeyChan) {
	keyMutex.Lock()
	defer keyMutex.Unlock()
	if chans, ok := keyWatcher[kc.Key]; ok {
		for i, c := range chans {
			if kc.Chan == c {
				keyWatcher[kc.Key] = append(chans[:i-1], chans[i:]...)
			}
		}
	}

}

// WaitKey waits for a key to be updated or expired
func WaitKey(key, value string, timeout time.Duration) bool {
	kw := &KeyChan{
		Key:  key,
		Chan: make(chan bool, 1),
	}

	addKeyChan(kw)

	val, err := GetString(key)
	if err != nil || val != value {
		if err != nil {
			log.Printf("poop: %#v\n", err)
		}
		return true // as mentioned, we don't care about the channels...
	}

	select {
	case <-kw.Chan:
		newVal, _ := GetString(key)
		return newVal != value

	case <-time.After(timeout):
		log.Print("timeout...")
		return false
	}
}
