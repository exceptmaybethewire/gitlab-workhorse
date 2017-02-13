package redis

import (
	"log"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
)

var (
	keyWatcher = make(map[string][]chan bool)
	keyMutex   sync.Mutex
)

const (
	keyPubEventSet     = "__keyevent@*__:set"
	keyPubEventExpired = "__keyevent@*__:expired"
)

// KeyChan holds a key and a channel
type KeyChan struct {
	Key  string
	Chan chan bool
}

func redisWorkerInner(conn redis.Conn) {
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
			return
		}
	}
}

func redisWorker(wg *sync.WaitGroup) {
	wg.Done()

	log.Print("redisWorker running")

	for {
		conn, err := redisDialFunc()
		if err == nil || conn != nil {
			redisWorkerInner(conn)
		} else {
			time.Sleep(1 * time.Second)
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

func innerDelKeyChan(kc *KeyChan, chanList []chan bool) {
	if len(keyWatcher[kc.Key]) == 1 {
		delete(keyWatcher, kc.Key)
		return
	}
	for i, c := range chanList {
		if kc.Chan == c {
			keyWatcher[kc.Key] = append(chanList[:i], chanList[i+1:]...)
			break
		}
	}
}

func delKeyChan(kc *KeyChan) {
	keyMutex.Lock()
	defer keyMutex.Unlock()
	if chans, ok := keyWatcher[kc.Key]; ok {
		innerDelKeyChan(kc, chans)
	}
}

// WaitKey waits for a key to be updated or expired
func WaitKey(key, value string, timeout time.Duration) bool {
	kw := &KeyChan{
		Key:  key,
		Chan: make(chan bool, 1),
	}

	addKeyChan(kw)
	defer delKeyChan(kw)

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
