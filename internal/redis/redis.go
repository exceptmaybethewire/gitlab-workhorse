package redis

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
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
	// Max Idle Connections in the pool.
	defaultMaxIdle = 1
	// Max Active Connections in the pool.
	defaultMaxActive = 1
	// Timeout for Read operations on the pool. 1 second is technically overkill,
	//  it's just for sanity.
	defaultReadTimeout = 1 * time.Second
	// Timeout for Write operations on the pool. 1 second is technically overkill,
	//  it's just for sanity.
	defaultWriteTimeout = 1 * time.Second
	// Timeout before killing Idle connections in the pool. 3 minutes seemed good.
	//  If you _actually_ hit this timeout often, you should consider turning of
	//  redis-support since it's not necessary at that point...
	defaultIdleTimeout = 3 * time.Minute
	// KeepAlivePeriod is to keep a TCP connection open for an extended period of
	//  time without being killed. This is used both in the pool, and in the
	//  worker-connection.
	//  See https://en.wikipedia.org/wiki/Keepalive#TCP_keepalive for more
	//  information.
	defaultKeepAlivePeriod = 5 * time.Minute
)

var (
	totalConnections = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_redis_total_connections",
			Help: "How many connections gitlab-workhorse has opened in total. Can be used to track Redis connection rate for this process",
		},
	)
	errorConnections = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_redis_errors",
			Help: "How many connections gitlab-workhorse has failed in total. Can be used to track Redis connection error rate for this process",
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
		h := url.URL.Host
		log.Printf("redis: using sentinel %q", h)
		addrs = append(addrs, h)
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

var poolDialFunc func() (redis.Conn, error)
var workerDialFunc func() (redis.Conn, error)

func timeoutDialOptions(cfg *config.RedisConfig) []redis.DialOption {
	readTimeout := defaultReadTimeout
	writeTimeout := defaultWriteTimeout

	if cfg != nil {
		if cfg.ReadTimeout != nil {
			readTimeout = cfg.ReadTimeout.Duration
		}

		if cfg.WriteTimeout != nil {
			writeTimeout = cfg.WriteTimeout.Duration
		}
	}
	return []redis.DialOption{
		redis.DialReadTimeout(readTimeout),
		redis.DialWriteTimeout(writeTimeout),
	}
}

func dialOptionsBuilder(cfg *config.RedisConfig, setTimeouts bool) []redis.DialOption {
	var dopts []redis.DialOption
	if setTimeouts {
		dopts = timeoutDialOptions(cfg)
	}
	if cfg == nil {
		return dopts
	}
	if cfg.Password != "" {
		dopts = append(dopts, redis.DialPassword(cfg.Password))
	}
	if cfg.DB != nil {
		dopts = append(dopts, redis.DialDatabase(*cfg.DB))
	}
	return dopts
}

func keepAliveDialer(timeout time.Duration) func(string, string) (net.Conn, error) {
	return func(network, address string) (net.Conn, error) {
		addr, err := net.ResolveTCPAddr(network, address)
		if err != nil {
			return nil, err
		}
		tc, err := net.DialTCP(network, nil, addr)
		if err != nil {
			return nil, err
		}
		if err := tc.SetKeepAlive(true); err != nil {
			return nil, err
		}
		if err := tc.SetKeepAlivePeriod(timeout); err != nil {
			return nil, err
		}
		return tc, nil
	}
}

type redisDialerFunc func() (redis.Conn, error)

func sentinelDialer(dopts []redis.DialOption, keepAlivePeriod time.Duration) redisDialerFunc {
	return func() (redis.Conn, error) {
		address, err := sntnl.MasterAddr()
		if err != nil {
			return nil, err
		}
		dopts = append(dopts, redis.DialNetDial(keepAliveDialer(keepAlivePeriod)))
		return redisDial("tcp", address, dopts...)
	}
}

func defaultDialer(dopts []redis.DialOption, keepAlivePeriod time.Duration, url url.URL) redisDialerFunc {
	return func() (redis.Conn, error) {
		if url.Scheme == "unix" {
			return redisDial(url.Scheme, url.Path, dopts...)
		}
		dopts = append(dopts, redis.DialNetDial(keepAliveDialer(keepAlivePeriod)))
		return redisDial(url.Scheme, url.Host, dopts...)
	}
}

func redisDial(network, address string, options ...redis.DialOption) (redis.Conn, error) {
	log.Printf("redis: dialing %q, %q", network, address)
	return redis.Dial(network, address, options...)
}

func countDialer(dialer redisDialerFunc) redisDialerFunc {
	return func() (redis.Conn, error) {
		c, err := dialer()
		if err == nil {
			totalConnections.Inc()
		}
		if err != nil {
			errorConnections.Inc()
		}
		return c, err
	}
}

// DefaultDialFunc should always used. Only exception is for unit-tests.
func DefaultDialFunc(cfg *config.RedisConfig, setReadTimeout bool) func() (redis.Conn, error) {
	keepAlivePeriod := defaultKeepAlivePeriod
	if cfg.KeepAlivePeriod != nil {
		keepAlivePeriod = cfg.KeepAlivePeriod.Duration
	}
	dopts := dialOptionsBuilder(cfg, setReadTimeout)
	if sntnl != nil {
		return countDialer(sentinelDialer(dopts, keepAlivePeriod))
	}
	return countDialer(defaultDialer(dopts, keepAlivePeriod, cfg.URL.URL))
}

// Configure redis-connection
func Configure(cfg *config.RedisConfig, dialFunc func(*config.RedisConfig, bool) func() (redis.Conn, error)) {
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
	workerDialFunc = dialFunc(cfg, false)
	poolDialFunc = dialFunc(cfg, true)
	pool = &redis.Pool{
		MaxIdle:     maxIdle,            // Keep at most X hot connections
		MaxActive:   maxActive,          // Keep at most X live connections, 0 means unlimited
		IdleTimeout: defaultIdleTimeout, // X time until an unused connection is closed
		Dial:        poolDialFunc,
		Wait:        true,
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
		return "", fmt.Errorf("redis: could not get connection from pool")
	}
	defer conn.Close()

	return redis.String(conn.Do("GET", key))
}
