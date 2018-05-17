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
	require := require.New(t)

	stub, ts := StartObjectStore()
	defer ts.Close()

	path := "/my-multipart"
	err := stub.InitiateMultipartUpload(path)
	require.NoError(err)

	err = stub.InitiateMultipartUpload(path)
	require.Error(err, "second attempt to open the same MultipartUpload")
}

func TestObjectStoreCompleteMultipartUpload(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	stub, ts := StartObjectStore()
	defer ts.Close()

	objectURL := ts.URL + ObjectPath
	parts := []struct {
		number     int
		content    string
		contentMD5 string
	}{
		{
			number:     1,
			content:    "first part",
			contentMD5: "550cf6b6e60f65a0e3104a26e70fea42",
		}, {
			number:     2,
			content:    "second part",
			contentMD5: "920b914bca0a70780b40881b8f376135",
		},
	}

	stub.InitiateMultipartUpload(ObjectPath)

	require.NotNil(stub.multipart[ObjectPath])
	assert.Equal(0, stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())

	// Workhorse knows nothing about S3 MultipartUpload, it receives some URLs
	//  from GitLab-rails and PUTs chunk of data to each of them.
	// Then it completes the upload with a final POST
	partPutURLs := []string{
		fmt.Sprintf("%s?partNumber=%d", objectURL, 1),
		fmt.Sprintf("%s?partNumber=%d", objectURL, 2),
	}
	completePostURL := objectURL

	for i, partPutURL := range partPutURLs {
		part := parts[i]

		req, err := http.NewRequest(http.MethodPut, partPutURL, strings.NewReader(part.content))
		require.NoError(err)

		_, err = http.DefaultClient.Do(req)
		require.NoError(err)

		assert.Equal(i+1, stub.PutsCnt())
		assert.Equal(0, stub.DeletesCnt())
		assert.Equal(part.contentMD5, stub.multipart[ObjectPath][part.number], "Part %d was not uploaded into ObjectStorage", part.number)
		assert.Empty(stub.GetObjectMD5(ObjectPath), "Part %d was mistakenly uploaded as a single object", part.number)
		assert.True(stub.IsMultipartUpload(ObjectPath), "MultipartUpload completed or aborted")
	}

	completeBody := fmt.Sprintf(`<CompleteMultipartUpload>
		<Part>
			<PartNumber>1</PartNumber>
			<ETag>%s</ETag>
		</Part>
		<Part>
			<PartNumber>2</PartNumber>
			<ETag>%s</ETag>
		</Part>
	</CompleteMultipartUpload>`, parts[0].contentMD5, parts[1].contentMD5)
	req, err := http.NewRequest(http.MethodPost, completePostURL, strings.NewReader(completeBody))
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	assert.Equal(len(parts), stub.PutsCnt())
	assert.Equal(0, stub.DeletesCnt())
	assert.NotEmpty(stub.GetObjectMD5(ObjectPath), "MultipartUpload not completed")
	assert.False(stub.IsMultipartUpload(ObjectPath), "MultipartUpload is still in progress")
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
	assert.Equal(ObjectMD5, stub.multipart[ObjectPath][1], "Part was not uploaded into ObjectStorage")
	assert.Empty(stub.GetObjectMD5(ObjectPath), "Part was mistakenly uploaded as a single object")
	assert.True(stub.IsMultipartUpload(ObjectPath), "MultipartUpload completed or aborted")

	req, err = http.NewRequest(http.MethodDelete, objectURL, nil)
	require.NoError(err)

	_, err = http.DefaultClient.Do(req)
	require.NoError(err)

	assert.Equal(1, stub.PutsCnt())
	assert.Equal(1, stub.DeletesCnt())
	assert.Empty(stub.GetObjectMD5(ObjectPath), "MultiUpload has been completed")
	assert.False(stub.IsMultipartUpload(ObjectPath), "MultiUpload is still in progress")
}
