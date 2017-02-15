package redis

import (
	"log"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	keyWatcher = make(map[string][]chan bool)
	keyMutex   sync.Mutex
)

var (
	keyWatchers = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gitlab_workhorse_internal_redis_keywatchers",
			Help: "The number of keys that is being watched by gitlab-workhorse",
		},
	)
)

func init() {
	prometheus.MustRegister(keyWatchers)
}

const (
	keyPubEventSet     = "__keyevent@*__:set"
	keyPubEventExpired = "__keyevent@*__:expired"

	redisReconnectWaitTime = 1 * time.Second
)

// KeyChan holds a key and a channel
type KeyChan struct {
	Key  string
	Chan chan bool
}

func redisWorkerInner(conn redis.Conn) {
	defer func() {
		conn.Close()
		openConnections.Dec()
	}()
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
			notifyChanWatchers(string(v.Data))
		case error:
			return
		}
	}
}

// Process redis subscriptions
func Process() {
	go redisWorker()
}

func redisWorker() {
	log.Print("redisWorker running")

	for {
		conn, err := redisDialFunc()
		if err == nil {
			totalConnections.Inc()
			openConnections.Inc()
			redisWorkerInner(conn)
		} else {
			time.Sleep(redisReconnectWaitTime)
		}
	}
}

func notifyChanWatchers(key string) {
	keyMutex.Lock()
	defer keyMutex.Unlock()
	if chanList, ok := keyWatcher[key]; ok {
		for _, c := range chanList {
			c <- true
			keyWatchers.Dec()
		}
		delete(keyWatcher, key)
	}
}

func addKeyChan(kc *KeyChan) {
	keyMutex.Lock()
	defer keyMutex.Unlock()
	keyWatcher[kc.Key] = append(keyWatcher[kc.Key], kc.Chan)
	keyWatchers.Inc()
}

func delKeyChan(kc *KeyChan) {
	keyMutex.Lock()
	defer keyMutex.Unlock()
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

// WaitKey waits for a key to be updated or expired
//
// Returns true if the value has changed, otherwise false
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
			log.Printf("Failed to get value from Redis: %#v\n", err)
		}
		hitMissCounter.WithLabelValues("miss", key).Inc()
		return true
	}

	select {
	case <-kw.Chan:
		newVal, _ := GetString(key)
		hitMissCounter.WithLabelValues("miss", key).Inc()
		return newVal != value

	case <-time.After(timeout):
		hitMissCounter.WithLabelValues("hit", key).Inc()
		return false
	}
}
