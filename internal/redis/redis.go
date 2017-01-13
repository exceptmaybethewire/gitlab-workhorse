package redis

import (
	"time"

	"github.com/garyburd/redigo/redis"
)

// Client holds the redis-connection
type Client struct {
	Conn redis.Conn
}

// NewClient connects to redis returns a Client instance.
func NewClient(socket, password string, timeout time.Duration) (*Client, error) {
	connPass := redis.DialPassword("password")
	connTimeout := redis.DialReadTimeout(timeout)
	conn, err := redis.Dial("unix", socket, connPass, connTimeout)
	if err != nil {
		return nil, err
	}

	cli := new(Client)
	cli.Conn = conn
	return cli, nil
}

// SubscribeKey subscribes to a key, waits at most timeout seconds before returning NotChanged
func (c *Client) SubscribeKey(channel, key string) (bool, error) {
	psc := redis.PubSubConn{Conn: c.Conn}
	psc.Subscribe(channel)
	for {
		switch v := psc.Receive().(type) {
		case redis.Message:
			if string(v.Data) == key {
				return true, nil
			}
		case error:
			return false, v
		}
	}
}
