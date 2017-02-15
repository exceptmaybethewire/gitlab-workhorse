package redis

import (
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"

	"github.com/garyburd/redigo/redis"
	"github.com/rafaeljusto/redigomock"
	"github.com/stretchr/testify/assert"
)

// Setup a MockPool for Redis
//
// Returns a teardown-function and the mock-connection
func setupMockPool() (func(), *redigomock.Conn) {
	conn := redigomock.NewConn()
	redisDialFunc = func() (redis.Conn, error) {
		return conn, nil
	}
	pool = &redis.Pool{
		MaxIdle:     2,               // Keep at most X hot connections
		MaxActive:   0,               // Keep at most X live connections, 0 means unlimited
		IdleTimeout: 3 * time.Minute, // 3 Minutes until an unused connection is closed. Newer gonna be used, but it's nice to have just in case
		Dial: func() (redis.Conn, error) {
			return conn, nil
		},
	}
	return func() {
		pool = nil
		conn = nil
	}, conn
}

func TestConfigureNoConfig(t *testing.T) {
	Configure(nil)
	assert.Nil(t, pool, "Pool should be nil")
}

func TestConfigureMinimalConfig(t *testing.T) {
	cfg := &config.RedisConfig{URL: config.TomlURL{}, Password: ""}
	Configure(cfg)
	if assert.NotNil(t, pool, "Pool should not be nil") {
		assert.Equal(t, 1, pool.MaxIdle, "MaxIdle should be 5")
		assert.Equal(t, 1, pool.MaxActive, "MaxActive should be 0")
		assert.Equal(t, 3*time.Minute, pool.IdleTimeout, "IdleTimeout should be 50s")
	}
	pool = nil
}

func TestConfigureFullConfig(t *testing.T) {
	i, a, r := 4, 10, 3
	cfg := &config.RedisConfig{
		URL:         config.TomlURL{},
		Password:    "",
		MaxIdle:     &i,
		MaxActive:   &a,
		ReadTimeout: &r,
	}
	Configure(cfg)
	if assert.NotNil(t, pool, "Pool should not be nil") {
		assert.Equal(t, i, pool.MaxIdle, "MaxIdle should be 4")
		assert.Equal(t, a, pool.MaxActive, "MaxActive should be 10")
		assert.Equal(t, 3*time.Minute, pool.IdleTimeout, "IdleTimeout should be 50s")
	}
	pool = nil
}

func TestGetConnFail(t *testing.T) {
	conn := Get()
	assert.Nil(t, conn, "Expected `conn` to be nil")
}

func TestGetConnPass(t *testing.T) {
	teardown, _ := setupMockPool()
	defer teardown()
	conn := Get()
	assert.NotNil(t, conn, "Expected `conn` to be a redis.Conn")
}

func TestGetString(t *testing.T) {
	teardown, conn := setupMockPool()
	defer teardown()
	conn.Command("GET", "foobar").Expect("herpderp")
	str, err := GetString("foobar")
	if assert.Nil(t, err, "Expected `err` to be nil") {
		var derp string
		assert.IsType(t, derp, str, "Expected value to be a string")
		assert.Equal(t, "herpderp", str, "Expected it to be equal")
	}
}
