package test

import (
	"fmt"
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

func TestObjectStoreInitiateMultipartUpload(t *testing.T) {
	assert := assert.New(t)

	stub, ts := StartObjectStore()
	defer ts.Close()

	const path string = "/my-multipart"
	err := stub.InitiateMultipartUpload(path)
	assert.NoError(err)

	err = stub.InitiateMultipartUpload(path)
	assert.Error(err, "second attempt to open the same MultipartUpload")
}

func TestObjectStoreCompleteMultipartUpload(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	stub, ts := StartObjectStore()
	defer ts.Close()

	stub.InitiateMultipartUpload(ObjectPath)

	require.NotNil(stub.multipart[ObjectPath])
	assert.Equal(0, stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())

	objectURL := ts.URL + ObjectPath

	for _, i := range []int{1, 2} {
		req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s?partNumber=%d", objectURL, i), strings.NewReader(ObjectContent))
		require.NoError(err)

		_, err = http.DefaultClient.Do(req)
		require.NoError(err)

		assert.Equal(i, stub.PutsCnt())
		assert.Equal(0, stub.DeletesCnt())
		assert.Equal(ObjectMD5, stub.multipart[ObjectPath][i])
		assert.Empty(stub.GetObjectMD5(ObjectPath))
		assert.True(stub.IsMultipartUpload(ObjectPath))
	}

	completeBody := fmt.Sprintf(`<CompleteMultipartUpload>
		<Part>
			<PartNumber>1</PartNumber>
			<ETag>%[1]s</ETag>
		</Part>
		<Part>
			<PartNumber>2</PartNumber>
			<ETag>%[1]s</ETag>
		</Part>
	</CompleteMultipartUpload>`, ObjectMD5)
	req, err := http.NewRequest(http.MethodPost, objectURL, strings.NewReader(completeBody))
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	assert.Equal(2, stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())
	assert.NotEmpty(stub.GetObjectMD5(ObjectPath))
	assert.False(stub.IsMultipartUpload(ObjectPath))
}

func TestObjectStoreAbortMultipartUpload(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	stub, ts := StartObjectStore()
	defer ts.Close()

	stub.InitiateMultipartUpload(ObjectPath)

	require.NotNil(stub.multipart[ObjectPath])
	assert.Equal(0, stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())

	objectURL := ts.URL + ObjectPath

	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s?partNumber=%d", objectURL, 1), strings.NewReader(ObjectContent))
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	assert.Equal(1, stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())
	assert.Equal(ObjectMD5, stub.multipart[ObjectPath][1])
	assert.Empty(stub.GetObjectMD5(ObjectPath))
	assert.True(stub.IsMultipartUpload(ObjectPath))

	req, err = http.NewRequest(http.MethodDelete, objectURL, nil)
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	assert.Equal(1, stub.PutsCnt())
	assert.Equal(1, stub.DeletesCnt())
	assert.Empty(stub.GetObjectMD5(ObjectPath))
	assert.False(stub.IsMultipartUpload(ObjectPath))
}
