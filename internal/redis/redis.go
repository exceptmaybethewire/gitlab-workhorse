package redis

import (
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"

	"github.com/garyburd/redigo/redis"
)

var pool *redis.Pool

func Configure(cfg *config.RedisConfig) {
	maxIdle := 5
	if cfg.MaxIdle != nil {
		maxIdle = *cfg.MaxIdle
	}
	maxActive := 0
	if cfg.MaxActive != nil {
		maxActive = *cfg.MaxActive
	}
	readTimeout := time.Duration(50)
	if cfg.ReadTimeout != nil {
		readTimeout = time.Duration(*cfg.ReadTimeout)
	}
	pool = &redis.Pool{
		MaxIdle:     maxIdle,         // Keep at most X hot connections
		MaxActive:   maxActive,       // Keep at most X live connections, 0 means unlimited
		IdleTimeout: 3 * time.Minute, // 3 Minutes until an unused connection is closed. Newer gonna be used, but it's nice to have just in case
		Dial: func() (redis.Conn, error) {
			dopts := []redis.DialOption{redis.DialReadTimeout(readTimeout * time.Second)}
			if cfg.Password != "" {
				dopts = append(dopts, redis.DialPassword(cfg.Password))
			}
			return redis.Dial(cfg.URL.Scheme, cfg.URL.Host, dopts...)
		},
	}
}

func Get() redis.Conn {
	if pool != nil {
		return pool.Get()
	}
	return nil
}

// WaitKey subscribes to a key and returns a channel, when it's done the
//  channel will have these values:
//  true: the key has changed
//  false: the key has not changed, OR the timeout was reached,
//         OR an error has occured (that is ugly, but efficient)
func WaitKey(channel, key string) chan bool {
	c := make(chan bool)

	go func(c chan bool) {
		conn := pool.Get()
		if conn == nil {
			c <- false
		}
		defer conn.Close()

		// NOTE: conn.Close() is deferred so no need to psc.Close()
		psc := redis.PubSubConn{Conn: conn}
		psc.Subscribe(channel)
		defer psc.Unsubscribe(channel) // This is however probably a good idea...
		for {
			switch v := psc.Receive().(type) {
			case redis.Message:
				if string(v.Data) == key {
					c <- true
				}
			case error:
				c <- false
			}
		}
	}(c)

	return c
}
