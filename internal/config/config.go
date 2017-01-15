package config

import (
	"io/ioutil"
	"net/url"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds configs
type Config struct {
	Backend                 *url.URL `toml:"-"`
	BackendRaw              string   `toml:"backend"`
	Version                 string   `toml:"-"`
	DocumentRoot            string
	DevelopmentMode         bool
	Socket                  string
	ProxyHeadersTimeout     time.Duration `toml:"-"`
	tomlProxyHeadersTimeout string        `toml:"proxy_header_timeout"`
	APILimit                uint
	APIQueueLimit           uint
	APIQueueTimeout         time.Duration `toml:"-"`
	tomlAPIQueueTimeout     string        `toml:"api_queue_timeout"`

	LogFile                 string
	ListenAddress           string `toml:"-"`
	ListenNetwork           string `toml:"-"`
	ListenUmask             int
	tomlListen              string `toml:"listen_url"`
	PprofListenAddress      string
	PrometheusListenAddress string
}

// LoadConfig from a file
func LoadConfig(filename string) (Config, error) {
	f, err := os.Open(filename)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	buf, err := ioutil.ReadAll(f)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err = toml.Unmarshal(buf, &config); err != nil {
		return Config{}, err
	}
	if config.tomlAPIQueueTimeout != "" {
		config.APIQueueTimeout, err = time.ParseDuration(config.tomlAPIQueueTimeout)
		if err != nil {
			return Config{}, err
		}
	}
	if config.tomlProxyHeadersTimeout != "" {
		config.ProxyHeadersTimeout, err = time.ParseDuration(config.tomlProxyHeadersTimeout)
		if err != nil {
			return Config{}, err
		}
	}
	if config.tomlListen != "" {
		url, err := url.Parse(config.tomlListen)
		if err != nil {
			return Config{}, nil
		}
		config.ListenNetwork = url.Scheme
		config.ListenAddress = url.Host
	}
	return config, nil
}
