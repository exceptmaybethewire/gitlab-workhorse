package redis

import (
	"testing"
	"time"

	"github.com/rafaeljusto/redigomock"
	"github.com/stretchr/testify/assert"
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

	go Process()
	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubEventSet).
		Expect(createSubscribeMessage(keyPubEventSet))
	mconn.Command("PUNSUBSCRIBE", keyPubEventSet).
		Expect(createUnsubscribeMessage(keyPubEventSet))
	mconn.Command("GET", "foobar:10").
		Expect("something").
		Expect("somethingelse")
	mconn.ReceiveWait = true

	mconn.AddSubscriptionMessage(createSubscriptionMessage(keyPubEventSet, "__keyevent@0__:set", "foobar:10"))

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val, err := WatchKey("foobar:10", "something", time.Duration(1*time.Second))
	assert.NoError(t, err, "Expected no error")
	assert.Equal(t, WatchKeyStatusSeenChange, val, "Expected value to change")
}

func TestWatchKeyNoChange(t *testing.T) {
	t.Log("No Change")
	td, mconn := setupMockPool()
	defer td()

	go Process()
	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubEventSet).
		Expect(createSubscribeMessage(keyPubEventSet))
	mconn.Command("PUNSUBSCRIBE", keyPubEventSet).
		Expect(createUnsubscribeMessage(keyPubEventSet))
	mconn.Command("GET", "foobar:10").
		Expect("something").
		Expect("something")
	mconn.ReceiveWait = true

	mconn.AddSubscriptionMessage(createSubscriptionMessage(keyPubEventSet, "__keyevent@0__:set", "foobar:10"))

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val, err := WatchKey("foobar:10", "something", time.Duration(1*time.Second))
	assert.NoError(t, err, "Expected no error")
	assert.Equal(t, WatchKeyStatusNoChange, val, "Expected notification without change to value")
}

func TestWatchKeyTimeout(t *testing.T) {
	t.Log("Timeout")
	td, mconn := setupMockPool()
	defer td()

	go Process()
	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubEventSet).
		Expect(createSubscribeMessage(keyPubEventSet))
	mconn.Command("PUNSUBSCRIBE", keyPubEventSet).
		Expect(createUnsubscribeMessage(keyPubEventSet))
	mconn.Command("GET", "foobar:10").
		Expect("something").
		Expect("something")
	mconn.ReceiveWait = true

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val, err := WatchKey("foobar:10", "something", time.Duration(1*time.Second))
	assert.NoError(t, err, "Expected no error")
	assert.Equal(t, WatchKeyStatusTimeout, val, "Expected value to not change")
}

func TestWatchKeyAlreadyChanged(t *testing.T) {
	t.Log("Already Changed")
	td, mconn := setupMockPool()
	defer td()

	go Process()
	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubEventSet).
		Expect(createSubscribeMessage(keyPubEventSet))
	mconn.Command("PUNSUBSCRIBE", keyPubEventSet).
		Expect(createUnsubscribeMessage(keyPubEventSet))
	mconn.Command("GET", "foobar:10").
		Expect("somethingelse").
		Expect("somethingelse")
	mconn.ReceiveWait = true

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val, err := WatchKey("foobar:10", "something", time.Duration(1*time.Second))
	assert.NoError(t, err, "Expected no error")
	assert.Equal(t, WatchKeyStatusAlreadyChanged, val, "Expected value to have already changed")
}
