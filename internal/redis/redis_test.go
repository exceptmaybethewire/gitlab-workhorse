package redis

import (
	"testing"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/rafaeljusto/redigomock"
	"github.com/stretchr/testify/assert"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// Setup a MockPool for Redis
//
// Returns a teardown-function and the mock-connection
func setupMockPool() (*redigomock.Conn, func()) {
	conn := redigomock.NewConn()
	cfg := &config.RedisConfig{URL: config.TomlURL{}}
	Configure(cfg, func(_ *config.RedisConfig, _ bool) func() (redis.Conn, error) {
		return func() (redis.Conn, error) {
			return conn, nil
		}
	})
	return conn, func() {
		pool = nil
	}
}

func TestConfigureNoConfig(t *testing.T) {
	pool = nil
	Configure(nil, nil)
	assert.Nil(t, pool, "Pool should be nil")
}

func TestConfigureMinimalConfig(t *testing.T) {
	cfg := &config.RedisConfig{URL: config.TomlURL{}, Password: ""}
	Configure(cfg, DefaultDialFunc)
	if assert.NotNil(t, pool, "Pool should not be nil") {
		assert.Equal(t, 1, pool.MaxIdle)
		assert.Equal(t, 1, pool.MaxActive)
		assert.Equal(t, 3*time.Minute, pool.IdleTimeout)
	}
	pool = nil
}

func TestConfigureFullConfig(t *testing.T) {
	i, a := 4, 10
	r := config.TomlDuration{Duration: 3}
	cfg := &config.RedisConfig{
		URL:         config.TomlURL{},
		Password:    "",
		MaxIdle:     &i,
		MaxActive:   &a,
		ReadTimeout: &r,
	}
	Configure(cfg, DefaultDialFunc)
	if assert.NotNil(t, pool, "Pool should not be nil") {
		assert.Equal(t, i, pool.MaxIdle)
		assert.Equal(t, a, pool.MaxActive)
		assert.Equal(t, 3*time.Minute, pool.IdleTimeout)
	}
	pool = nil
}

func TestGetConnFail(t *testing.T) {
	conn := Get()
	assert.Nil(t, conn, "Expected `conn` to be nil")
}

func TestGetConnPass(t *testing.T) {
	_, teardown := setupMockPool()
	defer teardown()
	conn := Get()
	assert.NotNil(t, conn, "Expected `conn` to be non-nil")
}

func TestGetStringPass(t *testing.T) {
	conn, teardown := setupMockPool()
	defer teardown()
	conn.Command("GET", "foobar").Expect("baz")
	str, err := GetString("foobar")
	if assert.NoError(t, err, "Expected `err` to be nil") {
		var value string
		assert.IsType(t, value, str, "Expected value to be a string")
		assert.Equal(t, "baz", str, "Expected it to be equal")
	}
}

func TestGetStringFail(t *testing.T) {
	_, err := GetString("foobar")
	assert.Error(t, err, "Expected error when not connected to redis")
}

func TestSentinelConnNoSentinel(t *testing.T) {
	s := sentinelConn("", []config.TomlURL{})

	assert.Nil(t, s, "Sentinel without urls should return nil")
}

func TestSentinelConnTwoURLs(t *testing.T) {
	addrs := []string{"10.0.0.1:12345", "10.0.0.2:12345"}
	var sentinelUrls []config.TomlURL

	for _, a := range addrs {
		parsedURL := helper.URLMustParse(`tcp://` + a)
		sentinelUrls = append(sentinelUrls, config.TomlURL{URL: *parsedURL})
	}

	s := sentinelConn("foobar", sentinelUrls)
	assert.Equal(t, len(addrs), len(s.Addrs))

	for i := range addrs {
		assert.Equal(t, addrs[i], s.Addrs[i])
	}
}

func TestDialOptionsBuildersPassword(t *testing.T) {
	dopts := dialOptionsBuilder(&config.RedisConfig{Password: "foo"}, false)
	assert.Equal(t, 1, len(dopts))
}

func TestDialOptionsBuildersSetTimeouts(t *testing.T) {
	dopts := dialOptionsBuilder(nil, true)
	assert.Equal(t, 2, len(dopts))
}

func TestDialOptionsBuildersSetTimeoutsConfig(t *testing.T) {
	cfg := &config.RedisConfig{
		ReadTimeout:  &config.TomlDuration{Duration: time.Second * time.Duration(15)},
		WriteTimeout: &config.TomlDuration{Duration: time.Second * time.Duration(15)},
	}
	dopts := dialOptionsBuilder(cfg, true)
	assert.Equal(t, 2, len(dopts))
}

func TestDialOptionsBuildersSelectDB(t *testing.T) {
	db := 3
	dopts := dialOptionsBuilder(&config.RedisConfig{DB: &db}, false)
	assert.Equal(t, 1, len(dopts))
}
