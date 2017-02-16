package redis

import (
	"testing"
	"time"

	"github.com/rafaeljusto/redigomock"
	"github.com/stretchr/testify/assert"
)

func createSubscriptionMessage(key, found, data string) []interface{} {
	values := []interface{}{}
	values = append(values, interface{}([]byte("pmessage")))
	values = append(values, interface{}([]byte(key)))
	values = append(values, interface{}([]byte(found)))
	values = append(values, interface{}([]byte(data)))
	return values
}

func createSubscribeMessage(key string) []interface{} {
	values := []interface{}{}
	values = append(values, interface{}([]byte("psubscribe")))
	values = append(values, interface{}([]byte(key)))
	values = append(values, interface{}([]byte("1")))
	return values
}

func TestWaitKeyChanging(t *testing.T) {
	td, mconn := setupMockPool()
	defer td()

	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubEventSet).
		Expect(createSubscribeMessage(keyPubEventSet))
	mconn.Command("PSUBSCRIBE", keyPubEventExpired).
		Expect(createSubscribeMessage(keyPubEventExpired))
	mconn.Command("GET", "foobar:10").
		Expect("herpderp").
		Expect("herpderp1")
	mconn.ReceiveWait = true

	Process()

	mconn.AddSubscriptionMessage(createSubscriptionMessage(keyPubEventSet, "__keyevent@0__:set", "foobar:10"))

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val := WaitKey("foobar:10", "herpderp", time.Duration(4*time.Second))
	assert.True(t, val, "Expected value to change")
}

func TestWaitKeyNotChanging(t *testing.T) {
	td, mconn := setupMockPool()
	defer td()

	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubEventSet).
		Expect(createSubscribeMessage(keyPubEventSet))
	mconn.Command("PSUBSCRIBE", keyPubEventExpired).
		Expect(createSubscribeMessage(keyPubEventExpired))
	mconn.Command("GET", "foobar:10").
		Expect("herpderp").
		Expect("herpderp")
	mconn.ReceiveWait = true

	Process()

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val := WaitKey("foobar:10", "herpderp", time.Duration(4*time.Second))
	assert.False(t, val, "Expected value to not change")
}

func TestWaitKeyAlreadyChanged(t *testing.T) {
	td, mconn := setupMockPool()
	defer td()

	// Setup the initial subscription message
	mconn.Command("PSUBSCRIBE", keyPubEventSet).
		Expect(createSubscribeMessage(keyPubEventSet))
	mconn.Command("PSUBSCRIBE", keyPubEventExpired).
		Expect(createSubscribeMessage(keyPubEventExpired))
	mconn.Command("GET", "foobar:10").
		Expect("herpderp1").
		Expect("herpderp1")
	mconn.ReceiveWait = true

	Process()

	// ACTUALLY Fill the buffers
	go func(mconn *redigomock.Conn) {
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
		mconn.ReceiveNow <- true
	}(mconn)

	val := WaitKey("foobar:10", "herpderp", time.Duration(4*time.Second))
	assert.True(t, val, "Expected value to change")
}
