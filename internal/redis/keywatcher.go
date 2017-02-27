package redis

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/jpillora/backoff"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	keyWatcher            = make(map[string][]chan string)
	keyWatcherMutex       sync.Mutex
	redisReconnectTimeout = backoff.Backoff{
		//These are the defaults
		Min:    100 * time.Millisecond,
		Max:    60 * time.Second,
		Factor: 2,
		Jitter: true,
	}
	keyWatchers = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gitlab_workhorse_keywatcher_keywatchers",
			Help: "The number of keys that is being watched by gitlab-workhorse",
		},
	)
)

func init() {
	prometheus.MustRegister(keyWatchers)
}

const (
	keyPubSpacePrefix = "__keyspace@*__:"
	keyPubSpaceRunner = keyPubSpacePrefix + "runner:build_queue:*"
	promStatusMiss    = "miss"
	promStatusHit     = "hit"
)

// KeyChan holds a key and a channel
type KeyChan struct {
	Key  string
	Chan chan string
}

func processInner(conn redis.Conn) {
	defer conn.Close()
	psc := redis.PubSubConn{Conn: conn}
	if err := psc.PSubscribe(keyPubSpaceRunner); err != nil {
		return
	}
	defer psc.PUnsubscribe(keyPubSpaceRunner)

	for {
		switch v := psc.Receive().(type) {
		case redis.PMessage:
			key := strings.TrimPrefix(string(v.Channel), keyPubSpacePrefix)
			notifyChanWatchers(key)
		case error:
			return
		}
	}
}

// Process redis subscriptions
//
// NOTE: There Can Only Be One!
// Reconnects is reconnect = true
func Process(reconnect bool) {
	log.Print("Processing redis queue")

	for {
		log.Println("Connecting to redis")
		conn, err := redisDialFunc()
		if err != nil && reconnect {
			time.Sleep(redisReconnectTimeout.Duration())
			continue
		}
		processInner(conn)
		redisReconnectTimeout.Reset()
		if !reconnect {
			return
		}
	}
}

func notifyChanWatchers(key string) {
	keyWatcherMutex.Lock()
	defer keyWatcherMutex.Unlock()
	if chanList, ok := keyWatcher[key]; ok {
		currentValue, err := GetString(key)
		if err != nil {
			// How do we handle error here? :D
			//return WatchKeyStatusNoChange, fmt.Errorf("Failed to get value from Redis: %#v", err)
		}
		// NOTE: Because every client will (in most cases) have it's own key,
		//  this is basically redundant, but also quite cheap for len(chanList) == 1
		for _, c := range chanList {
			c <- currentValue
			keyWatchers.Dec()
		}
		delete(keyWatcher, key)
	}
}

func addKeyChan(kc *KeyChan) {
	keyWatcherMutex.Lock()
	defer keyWatcherMutex.Unlock()
	keyWatcher[kc.Key] = append(keyWatcher[kc.Key], kc.Chan)
	keyWatchers.Inc()
}

func delKeyChan(kc *KeyChan) {
	keyWatcherMutex.Lock()
	defer keyWatcherMutex.Unlock()
	if chans, ok := keyWatcher[kc.Key]; ok {
		for i, c := range chans {
			if kc.Chan == c {
				keyWatcher[kc.Key] = append(chans[:i], chans[i+1:]...)
				keyWatchers.Dec()
				break
			}
		}
		if len(keyWatcher[kc.Key]) == 0 {
			delete(keyWatcher, kc.Key)
		}
	}
}

// WatchKeyStatus is used to tell how WatchKey returned
type WatchKeyStatus int

const (
	// WatchKeyStatusTimeout is returned when the watch timeout provided by the caller was exceeded
	WatchKeyStatusTimeout WatchKeyStatus = iota
	// WatchKeyStatusAlreadyChanged is returned when the value passed by the caller was never observed
	WatchKeyStatusAlreadyChanged
	// WatchKeyStatusSeenChange is returned when we have seen the value passed by the caller get changed
	WatchKeyStatusSeenChange
	// WatchKeyStatusNoChange is returned when the function had to return before observing a change.
	//  Also returned on errors.
	WatchKeyStatusNoChange
)

// WatchKey waits for a key to be updated or expired
func WatchKey(key, value string, timeout time.Duration) (WatchKeyStatus, error) {
	kw := &KeyChan{
		Key:  key,
		Chan: make(chan string, 1),
	}

	addKeyChan(kw)
	defer delKeyChan(kw)

	currentValue, err := GetString(key)
	if err != nil {
		return WatchKeyStatusNoChange, fmt.Errorf("Failed to get value from Redis: %#v", err)
	}
	if currentValue != value {
		return WatchKeyStatusAlreadyChanged, nil
	}

	select {
	case currentValue := <-kw.Chan:
		if currentValue == value {
			return WatchKeyStatusNoChange, nil
		}
		return WatchKeyStatusSeenChange, nil

	case <-time.After(timeout):
		return WatchKeyStatusTimeout, nil
	}
}
