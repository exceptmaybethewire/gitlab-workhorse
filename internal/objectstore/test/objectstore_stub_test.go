package test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/jfbus/httprs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObjectStoreStub(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stub, ts, err := StartObjectStore(ctx)
	require.NoError(err)

	assert.Equal(0, stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())

	objectURL := ts.URL + ObjectPath

	// PutObject
	req, err := http.NewRequest(http.MethodPut, objectURL, strings.NewReader(ObjectContent))
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	assert.Equal(1, stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())
	assert.Equal(ObjectMD5, stub.GetObjectMD5(ObjectPath))

	_, err = os.Stat(path.Join(stub.storage, ObjectPath))
	assert.NoError(err)

	//GetObject
	req, err = http.NewRequest(http.MethodGet, objectURL, nil)
	require.NoError(err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(err)

	rs := httprs.NewHttpReadSeeker(resp, http.DefaultClient)
	defer rs.Close()

	buff := make([]byte, ObjectSize-1)
	n, err := rs.ReadAt(buff, 1)
	assert.Equal(io.EOF, err)
	assert.Equal(ObjectSize-1, int64(n))
	assert.Equal(ObjectContent[1:], string(buff))

	// DeleteObject
	req, err = http.NewRequest(http.MethodDelete, objectURL, nil)
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	assert.Equal(1, stub.PutsCnt())
	assert.Equal(1, stub.DeletesCnt())
	_, err = os.Stat(path.Join(stub.storage, ObjectPath))
	assert.Error(err)
}

func TestObjectStoreStubDelete404(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stub, ts, err := StartObjectStore(ctx)
	require.NoError(err)

	objectURL := ts.URL + ObjectPath

	req, err := http.NewRequest(http.MethodDelete, objectURL, nil)
	require.NoError(err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(err)
	assert.Equal(404, resp.StatusCode)

	assert.Equal(0, stub.DeletesCnt())
}

func TestObjectStoreStubDisposal(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stub, ts, err := StartObjectStore(ctx)
	require.NoError(err)

	_, err = os.Stat(stub.storage)
	assert.NoError(err)

	objectURL := ts.URL + ObjectPath

	// PutObject
	req, err := http.NewRequest(http.MethodPut, objectURL, strings.NewReader(ObjectContent))
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	cancel()

	// Poll because the file removal is async
	for i := 0; i < 100; i++ {
		_, err = os.Stat(stub.storage)
		if err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	assert.True(os.IsNotExist(err), "Storage hasn't been deleted during cleanup")
}
