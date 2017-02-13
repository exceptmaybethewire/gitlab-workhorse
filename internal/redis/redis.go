package redis

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"

	sentinel "github.com/FZambia/go-sentinel"
	"github.com/garyburd/redigo/redis"
)

var pool *redis.Pool
var sntnl *sentinel.Sentinel

func sentinelConn(urls []config.TomlURL) *sentinel.Sentinel {
	if len(urls) == 0 {
		return nil
	}
	return &sentinel.Sentinel{
		Addrs: func(urls []config.TomlURL) []string {
			var addrs []string
			for _, url := range urls {
				addrs = append(addrs, url.URL.String())
			}
			return addrs
		}(urls),
		MasterName: "mymaster",
		Dial: func(addr string) (redis.Conn, error) {
			timeout := 500 * time.Millisecond
			c, err := redis.DialTimeout("tcp", addr, timeout, timeout, timeout)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	}
}

var redisDialFunc func() (redis.Conn, error)

func dialFunc(cfg *config.RedisConfig) func() (redis.Conn, error) {
	readTimeout := time.Duration(50 * time.Second)
	if cfg.ReadTimeout != nil {
		readTimeout = time.Duration(*cfg.ReadTimeout)
	}
	return func() (redis.Conn, error) {
		dopts := []redis.DialOption{redis.DialReadTimeout(readTimeout)}
		if cfg.Password != "" {
			dopts = append(dopts, redis.DialPassword(cfg.Password))
		}
		if sntnl != nil {
			address, err := sntnl.MasterAddr()
			if err != nil {
				return nil, err
			}
			return redis.Dial("tcp", address, dopts...)
		}
		return redis.Dial(cfg.URL.Scheme, cfg.URL.Host, dopts...)
	}
}

// Configure redis-connection
func Configure(cfg *config.RedisConfig) {
	if cfg == nil {
		return
	}
	maxIdle := 1
	if cfg.MaxIdle != nil {
		maxIdle = *cfg.MaxIdle
	}
	maxActive := 1
	if cfg.MaxActive != nil {
		maxActive = *cfg.MaxActive
	}
	sntnl = sentinelConn(cfg.Sentinel)
	redisDialFunc = dialFunc(cfg)
	pool = &redis.Pool{
		MaxIdle:     maxIdle,         // Keep at most X hot connections
		MaxActive:   maxActive,       // Keep at most X live connections, 0 means unlimited
		IdleTimeout: 3 * time.Minute, // 3 Minutes until an unused connection is closed. Newer gonna be used, but it's nice to have just in case
		Dial:        redisDialFunc,
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if sntnl == nil {
				return nil
			}
			if !sentinel.TestRole(c, "master") {
				return errors.New("Role check failed")
			} else {
				return nil
			}
		},
	}
}

// Process redis subscriptions
func Process() {
	var wg sync.WaitGroup
	wg.Add(1)
	go redisWorker(&wg)
	wg.Wait()
}

// Get a connection for the Redis-pool
func Get() redis.Conn {
	if pool != nil {
		return pool.Get()
	}
	return nil
}

// GetString fetches the value of a key in Redis as a string
func GetString(key string) (string, error) {
	conn := Get()
	if conn == nil {
		return "", fmt.Errorf("Not connected to redis")
	}
	defer conn.Close()
	return redis.String(conn.Do("GET", key))
}
