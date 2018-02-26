package test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObjectStoreStub(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	stub, ts := StartObjectStore()
	defer ts.Close()

	assert.Equal(0, stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())

	objectURL := ts.URL + ObjectPath

	req, err := http.NewRequest(http.MethodPut, objectURL, strings.NewReader(ObjectContent))
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	assert.Equal(1, stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())
	assert.Equal(ObjectMD5, stub.GetObjectMD5(ObjectPath))

	req, err = http.NewRequest(http.MethodDelete, objectURL, nil)
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	assert.Equal(1, stub.PutsCnt())
	assert.Equal(1, stub.DeletesCnt())
}

func TestObjectStoreStubDelete404(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	stub, ts := StartObjectStore()
	defer ts.Close()

	objectURL := ts.URL + ObjectPath

	req, err := http.NewRequest(http.MethodDelete, objectURL, nil)
	require.NoError(err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(err)
	assert.Equal(404, resp.StatusCode)

	assert.Equal(0, stub.DeletesCnt())
}
