package artifacts

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

func createTestZipArchive(t *testing.T) (bytes.Buffer, string) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	file, err := writer.CreateFormFile("file", "my.file")
	require.NoError(t, err)
	archive := zip.NewWriter(file)

	fileInArchive, err := archive.Create("test.file")
	require.NoError(t, err)
	fmt.Fprint(fileInArchive, "test")
	archive.Close()
	writer.Close()
	return buffer, writer.FormDataContentType()
}

func TestUploadHandlerSendingToExternalStorage(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	putCalledTimes := 0

	storeServerMux := http.NewServeMux()
	storeServerMux.HandleFunc("/url/put", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)

		data, err := ioutil.ReadAll(r.Body)
		require.NoError(t, err)

		_, err = zip.NewReader(bytes.NewReader(data), int64(len(data)))
		require.NoError(t, err)

		putCalledTimes++
		w.WriteHeader(200)
	})

	responseProcessor := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "store-id", r.FormValue("file.object_id"))
		assert.NotEmpty(t, r.FormValue("file.store_url"))
		w.WriteHeader(200)
	}

	storeServer := httptest.NewServer(storeServerMux)
	defer storeServer.Close()

	authResponse := api.Response{
		TempPath: tempPath,
		ObjectStore: api.RemoteObjectStore{
			StoreURL: storeServer.URL + "/url/put",
			ObjectID: "store-id",
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	buffer, contentType := createTestZipArchive(t)

	response := testUploadArtifacts(contentType, &buffer, t, ts)
	testhelper.AssertResponseCode(t, response, 200)
	assert.Equal(t, 1, putCalledTimes, "upload should be called only once")
}

func TestUploadHandlerSendingToExternalStorageAndInvalidStoreURLIsUsed(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	responseProcessor := func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("it should not be called")
	}

	authResponse := api.Response{
		TempPath: tempPath,
		ObjectStore: api.RemoteObjectStore{
			StoreURL: "http://localhost:12323/invalid/url",
			ObjectID: "store-id",
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	buffer, contentType := createTestZipArchive(t)

	response := testUploadArtifacts(contentType, &buffer, t, ts)
	testhelper.AssertResponseCode(t, response, 500)
}

func TestUploadHandlerSendingToExternalStorageAndItReturnsAnError(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	putCalledTimes := 0

	storeServerMux := http.NewServeMux()
	storeServerMux.HandleFunc("/url/put", func(w http.ResponseWriter, r *http.Request) {
		putCalledTimes++
		assert.Equal(t, "PUT", r.Method)
		w.WriteHeader(510)
	})

	responseProcessor := func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("it should not be called")
	}

	storeServer := httptest.NewServer(storeServerMux)
	defer storeServer.Close()

	authResponse := api.Response{
		TempPath: tempPath,
		ObjectStore: api.RemoteObjectStore{
			StoreURL: storeServer.URL + "/url/put",
			ObjectID: "store-id",
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	buffer, contentType := createTestZipArchive(t)

	response := testUploadArtifacts(contentType, &buffer, t, ts, DefaultObjectStoreTimeout)
	testhelper.AssertResponseCode(t, response, 500)
	assert.Equal(t, 1, putCalledTimes, "upload should be called only once")
}

func TestUploadHandlerSendingToExternalStorageAndSupportRequestTimeout(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	putCalledTimes := 0

	storeServerMux := http.NewServeMux()
	storeServerMux.HandleFunc("/url/put", func(w http.ResponseWriter, r *http.Request) {
		putCalledTimes++
		assert.Equal(t, "PUT", r.Method)
		time.Sleep(time.Minute)
		w.WriteHeader(510)
	})

	responseProcessor := func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("it should not be called")
	}

	storeServer := httptest.NewServer(storeServerMux)
	defer storeServer.Close()

	authResponse := api.Response{
		TempPath: tempPath,
		ObjectStore: api.RemoteObjectStore{
			StoreURL: storeServer.URL + "/url/put",
			ObjectID: "store-id",
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	buffer, contentType := createTestZipArchive(t)

	response := testUploadArtifacts(contentType, &buffer, t, ts, time.Second)
	testhelper.AssertResponseCode(t, response, 500)
	assert.Equal(t, 1, putCalledTimes, "upload should be called only once")
}
