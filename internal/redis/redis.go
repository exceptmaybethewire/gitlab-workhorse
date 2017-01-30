package redis

import (
	"log"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"

	"github.com/garyburd/redigo/redis"
)

var pool *redis.Pool

func Configure(cfg *config.RedisConfig) {
	if cfg == nil {
		return
	}
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

	log.Println("redis Configured, firing up redisWorker")

	// This is no longer only Configure, but StartService as well...
	go redisWorker()
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
	defer conn.Close()
	return redis.String(conn.Do("GET", key))
}
