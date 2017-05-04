package config

import (
	"net/url"
	"time"

	"github.com/BurntSushi/toml"
)

type TomlURL struct {
	url.URL
}

func (u *TomlURL) UnmarshalText(text []byte) error {
	temp, err := url.Parse(string(text))
	u.URL = *temp
	return err
}

type TomlDuration struct {
	time.Duration
}

func (d *TomlDuration) UnmarshalTest(text []byte) error {
	temp, err := time.ParseDuration(string(text))
	d.Duration = temp
	return err
}

type RedisConfig struct {
	URL             TomlURL
	Sentinel        []TomlURL
	SentinelMaster  string
	Password        string
	DB              *int
	ReadTimeout     *TomlDuration
	WriteTimeout    *TomlDuration
	KeepAlivePeriod *TomlDuration
	MaxIdle         *int
	MaxActive       *int
}

type Config struct {
	Redis                    *RedisConfig  `toml:"redis"`
	Backend                  *url.URL      `toml:"-"`
	Version                  string        `toml:"-"`
	DocumentRoot             string        `toml:"-"`
	DevelopmentMode          bool          `toml:"-"`
	Socket                   string        `toml:"-"`
	ProxyHeadersTimeout      time.Duration `toml:"-"`
	APILimit                 uint          `toml:"-"`
	APIQueueLimit            uint          `toml:"-"`
	APIQueueTimeout          time.Duration `toml:"-"`
	APICILongPollingDuration time.Duration `toml:"-"`
}

// LoadConfig from a file
func LoadConfig(filename string) (*Config, error) {
	cfg := &Config{}
	if _, err := toml.DecodeFile(filename, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
