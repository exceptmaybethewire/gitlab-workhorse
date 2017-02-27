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

const (
	defaultMaxIdle     = 1
	defaultMaxActive   = 1
	defaultReadTimeout = 50 * time.Second
	defaultIdleTimeout = 3 * time.Minute
)

var (
	totalConnections = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_redis_total_connections",
			Help: "How many connections gitlab-workhorse has opened in total. Can be used to track Redis connection rate for this process",
		},
	)
)

func init() {
	prometheus.MustRegister(
		totalConnections,
	)
}

func sentinelConn(master string, urls []config.TomlURL) *sentinel.Sentinel {
	if len(urls) == 0 {
		return nil
	}
	var addrs []string
	for _, url := range urls {
		addrs = append(addrs, url.URL.String())
	}
	return &sentinel.Sentinel{
		Addrs:      addrs,
		MasterName: master,
		Dial: func(addr string) (redis.Conn, error) {
			// This timeout is recommended for Sentinel-support according to the guidelines.
			//  https://redis.io/topics/sentinel-clients#redis-service-discovery-via-sentinel
			//  For every address it should try to connect to the Sentinel,
			//  using a short timeout (in the order of a few hundreds of milliseconds).
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

func dialOptionsBuilder(cfg *config.RedisConfig) []redis.DialOption {
	readTimeout := defaultReadTimeout
	if cfg.ReadTimeout != nil {
		readTimeout = time.Second * time.Duration(*cfg.ReadTimeout)
	}
	dopts := []redis.DialOption{redis.DialReadTimeout(readTimeout)}
	if cfg.Password != "" {
		dopts = append(dopts, redis.DialPassword(cfg.Password))
	}
	return dopts
}

// DefaultDialFunc should always used. Only exception is for unit-tests.
func DefaultDialFunc(cfg *config.RedisConfig) func() (redis.Conn, error) {
	dopts := dialOptionsBuilder(cfg)
	innerDial := func() (redis.Conn, error) {
		return redis.Dial(cfg.URL.Scheme, cfg.URL.Host, dopts...)
	}
	if sntnl != nil {
		innerDial = func() (redis.Conn, error) {
			address, err := sntnl.MasterAddr()
			if err != nil {
				return nil, err
			}
			return redis.Dial("tcp", address, dopts...)
		}
	}
	return func() (redis.Conn, error) {
		c, err := innerDial()
		if err == nil {
			totalConnections.Inc()
		}
		return c, err
	}
}

// Configure redis-connection
func Configure(cfg *config.RedisConfig, dialFunc func() (redis.Conn, error)) {
	if cfg == nil {
		return
	}
	maxIdle := defaultMaxIdle
	if cfg.MaxIdle != nil {
		maxIdle = *cfg.MaxIdle
	}
	maxActive := defaultMaxActive
	if cfg.MaxActive != nil {
		maxActive = *cfg.MaxActive
	}
	sntnl = sentinelConn(cfg.SentinelMaster, cfg.Sentinel)
	redisDialFunc = dialFunc
	pool = &redis.Pool{
		MaxIdle:     maxIdle,            // Keep at most X hot connections
		MaxActive:   maxActive,          // Keep at most X live connections, 0 means unlimited
		IdleTimeout: defaultIdleTimeout, // X time until an unused connection is closed
		Dial:        redisDialFunc,
	}
	if sntnl != nil {
		pool.TestOnBorrow = func(c redis.Conn, t time.Time) error {
			if !sentinel.TestRole(c, "master") {
				return errors.New("Role check failed")
			}
			return nil
		}
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
	defer func() {
		conn.Close()
	}()
	return redis.String(conn.Do("GET", key))
}
