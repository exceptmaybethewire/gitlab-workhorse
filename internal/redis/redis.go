package redis

import (
	"errors"
	"fmt"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"

	sentinel "github.com/FZambia/go-sentinel"
	"github.com/garyburd/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	pool  *redis.Pool
	sntnl *sentinel.Sentinel
)

var (
	totalConnections = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_internal_redis_total_connections",
			Help: "How many connections gitlab-workhorse has opened in total. Can be used to track Redis connection rate for this process",
		},
	)
	openConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gitlab_workhorse_internal_redis_open_connections",
			Help: "How many open connections gitlab-workhorse currently have",
		},
	)
	hitMissCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_internal_redis_cache_hit_miss",
			Help: "How many redis queries have been completed by gitlab-workhorse, partitioned by hit and miss",
		},
		[]string{"status", "token"},
	)
)

func init() {
	prometheus.MustRegister(
		totalConnections,
		openConnections,
		hitMissCounter,
	)
}

func sentinelConn(urls []config.TomlURL) *sentinel.Sentinel {
	if len(urls) == 0 {
		return nil
	}
	var addrs []string
	for _, url := range urls {
		addrs = append(addrs, url.URL.String())
	}
	return &sentinel.Sentinel{
		Addrs:      addrs,
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
	dopts := []redis.DialOption{redis.DialReadTimeout(readTimeout)}
	if cfg.Password != "" {
		dopts = append(dopts, redis.DialPassword(cfg.Password))
	}
	if sntnl != nil {
		return func() (c redis.Conn, err error) {
			var address string
			address, err = sntnl.MasterAddr()
			if err != nil {
				return
			}
			c, err = redis.Dial("tcp", address, dopts...)
			if err != nil {
				totalConnections.Inc()
			}
			return
		}
	}
	return func() (c redis.Conn, err error) {
		c, err = redis.Dial(cfg.URL.Scheme, cfg.URL.Host, dopts...)
		if err != nil {
			totalConnections.Inc()
		}
		return
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
			}
			return nil
		},
	}
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
	openConnections.Inc()
	defer func() {
		conn.Close()
		openConnections.Dec()
	}()
	return redis.String(conn.Do("GET", key))
}
