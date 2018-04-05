package objectstore_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/objectstore"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/objectstore/test"
)

const testTimeout = 10 * time.Second

func testObjectUploadNoErrors(t *testing.T, useDeleteURL bool) {
	assert := assert.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	osStub, ts, err := test.StartObjectStore(ctx)
	require.NoError(t, err)

	objectURL := ts.URL + test.ObjectPath
	var deleteURL string
	if useDeleteURL {
		deleteURL = objectURL
	}

	ctxRequest, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()

	object, err := objectstore.NewObject(ctxRequest, objectURL, deleteURL, testTimeout, test.ObjectSize)
	require.NoError(t, err)

	// copy data
	n, err := io.Copy(object, strings.NewReader(test.ObjectContent))
	assert.NoError(err)
	assert.Equal(test.ObjectSize, n, "Uploaded file mismatch")

	// close HTTP stream
	err = object.Close()
	assert.NoError(err)

	// Checking MD5 extraction
	assert.Equal(osStub.GetObjectMD5(test.ObjectPath), object.MD5())

	// Checking cleanup
	cancelRequest()
	assert.Equal(1, osStub.PutsCnt(), "Object hasn't been uploaded")

	var expectedDeleteCnt int
	if useDeleteURL {
		expectedDeleteCnt = 1
	}
	// Poll because the object removal is async
	for i := 0; i < 100; i++ {
		if osStub.DeletesCnt() == expectedDeleteCnt {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if useDeleteURL {
		assert.Equal(1, osStub.DeletesCnt(), "Object hasn't been deleted")
	} else {
		assert.Equal(0, osStub.DeletesCnt(), "Object has been deleted")
	}
}

func TestObjectUpload(t *testing.T) {
	t.Run("with delete URL", func(t *testing.T) { testObjectUploadNoErrors(t, true) })
	t.Run("without delete URL", func(t *testing.T) { testObjectUploadNoErrors(t, false) })
}

func TestObjectUpload404(t *testing.T) {
	assert := assert.New(t)

	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	objectURL := ts.URL + test.ObjectPath
	object, err := objectstore.NewObject(ctx, objectURL, "", testTimeout, test.ObjectSize)
	require.NoError(t, err)
	_, err = io.Copy(object, strings.NewReader(test.ObjectContent))

	assert.NoError(err)
	err = object.Close()
	assert.Error(err)
	_, isStatusCodeError := err.(objectstore.StatusCodeError)
	assert.True(isStatusCodeError, "Should fail with StatusCodeError")
	assert.Contains(err.Error(), "404")
}
