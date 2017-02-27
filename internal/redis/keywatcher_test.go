package redis

import (
	"testing"
	"time"

	"github.com/rafaeljusto/redigomock"
	"github.com/stretchr/testify/assert"
)

const (
	runnerToken = "10"
	runnerKey   = "runner:build_queue:" + runnerToken
	runnerSpace = keyPubSpacePrefix + runnerKey
)

func createSubscriptionMessage(key, found, data string) []interface{} {
	return []interface{}{
		[]byte("pmessage"),
		[]byte(key),
		[]byte(found),
		[]byte(data),
	}
}

func createSubscribeMessage(key string) []interface{} {
	return []interface{}{
		[]byte("psubscribe"),
		[]byte(key),
		[]byte("1"),
	}
}
func createUnsubscribeMessage(key string) []interface{} {
	return []interface{}{
		[]byte("punsubscribe"),
		[]byte(key),
		[]byte("1"),
	}
}

func TestWatchKeySeenChange(t *testing.T) {
	t.Log("Seen Change")
	td, mconn := setupMockPool()
	defer td()

	go Process(false)
	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubSpaceRunner).
		Expect(createSubscribeMessage(keyPubSpaceRunner))
	mconn.Command("PUNSUBSCRIBE", keyPubSpaceRunner).
		Expect(createUnsubscribeMessage(keyPubSpaceRunner))
	mconn.Command("GET", runnerKey).
		Expect("something").
		Expect("somethingelse")
	mconn.ReceiveWait = true

	mconn.AddSubscriptionMessage(createSubscriptionMessage(keyPubSpaceRunner, runnerSpace, "set"))

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val, err := WatchKey(runnerKey, "something", time.Duration(1*time.Second))
	assert.NoError(t, err, "Expected no error")
	assert.Equal(t, WatchKeyStatusSeenChange, val, "Expected value to change")
}

func TestWatchKeyNoChange(t *testing.T) {
	t.Log("No Change")
	td, mconn := setupMockPool()
	defer td()

	go Process(false)
	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubSpaceRunner).
		Expect(createSubscribeMessage(keyPubSpaceRunner))
	mconn.Command("PUNSUBSCRIBE", keyPubSpaceRunner).
		Expect(createUnsubscribeMessage(keyPubSpaceRunner))
	mconn.Command("GET", runnerKey).
		Expect("something").
		Expect("something")
	mconn.ReceiveWait = true

	mconn.AddSubscriptionMessage(createSubscriptionMessage(keyPubSpaceRunner, runnerSpace, "set"))

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val, err := WatchKey(runnerKey, "something", time.Duration(1*time.Second))
	assert.NoError(t, err, "Expected no error")
	assert.Equal(t, WatchKeyStatusNoChange, val, "Expected notification without change to value")
}

func TestWatchKeyTimeout(t *testing.T) {
	t.Log("Timeout")
	td, mconn := setupMockPool()
	defer td()

	go Process(false)
	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubSpaceRunner).
		Expect(createSubscribeMessage(keyPubSpaceRunner))
	mconn.Command("PUNSUBSCRIBE", keyPubSpaceRunner).
		Expect(createUnsubscribeMessage(keyPubSpaceRunner))
	mconn.Command("GET", runnerKey).
		Expect("something").
		Expect("something")
	mconn.ReceiveWait = true

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val, err := WatchKey(runnerKey, "something", time.Duration(1*time.Second))
	assert.NoError(t, err, "Expected no error")
	assert.Equal(t, WatchKeyStatusTimeout, val, "Expected value to not change")
}

func TestWatchKeyAlreadyChanged(t *testing.T) {
	t.Log("Already Changed")
	td, mconn := setupMockPool()
	defer td()

	go Process(false)
	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubSpaceRunner).
		Expect(createSubscribeMessage(keyPubSpaceRunner))
	mconn.Command("PUNSUBSCRIBE", keyPubSpaceRunner).
		Expect(createUnsubscribeMessage(keyPubSpaceRunner))
	mconn.Command("GET", runnerKey).
		Expect("somethingelse").
		Expect("somethingelse")
	mconn.ReceiveWait = true

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val, err := WatchKey(runnerKey, "something", time.Duration(1*time.Second))
	assert.NoError(t, err, "Expected no error")
	assert.Equal(t, WatchKeyStatusAlreadyChanged, val, "Expected value to have already changed")
}
