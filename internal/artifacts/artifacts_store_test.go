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

func createTestZipArchive(t *testing.T) []byte {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	fileInArchive, err := archive.Create("test.file")
	require.NoError(t, err)
	fmt.Fprint(fileInArchive, "test")
	archive.Close()
	return buffer.Bytes()
}

func createTestMultipartForm(t *testing.T, data []byte) (bytes.Buffer, string) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	file, err := writer.CreateFormFile("file", "my.file")
	require.NoError(t, err)
	file.Write(data)
	writer.Close()
	return buffer, writer.FormDataContentType()
}

func TestUploadHandlerSendingToExternalStorage(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	archiveData := createTestZipArchive(t)
	contentBuffer, contentType := createTestMultipartForm(t, archiveData)

	storeServerCalled := 0
	storeServerMux := http.NewServeMux()
	storeServerMux.HandleFunc("/url/put", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)

		receivedData, err := ioutil.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, archiveData, receivedData)

		storeServerCalled++
		w.WriteHeader(200)
	})

	responseProcessorCalled := 0
	responseProcessor := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "store-id", r.FormValue("file.object_id"))
		assert.NotEmpty(t, r.FormValue("file.store_url"))
		w.WriteHeader(200)
		responseProcessorCalled++
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

	response := testUploadArtifacts(contentType, &contentBuffer, t, ts)
	testhelper.AssertResponseCode(t, response, 200)
	assert.Equal(t, 1, storeServerCalled, "store should be called only once")
	assert.Equal(t, 1, responseProcessorCalled, "response processor should be called only once")
}

func TestUploadHandlerSendingToExternalStorageAndStorageServerUnreachable(t *testing.T) {
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

	archiveData := createTestZipArchive(t)
	contentBuffer, contentType := createTestMultipartForm(t, archiveData)

	response := testUploadArtifacts(contentType, &contentBuffer, t, ts)
	testhelper.AssertResponseCode(t, response, 500)
}

func TestUploadHandlerSendingToExternalStorageAndInvalidURLIsUsed(t *testing.T) {
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
			StoreURL: "htt:////invalid-url",
			ObjectID: "store-id",
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	archiveData := createTestZipArchive(t)
	contentBuffer, contentType := createTestMultipartForm(t, archiveData)

	response := testUploadArtifacts(contentType, &contentBuffer, t, ts)
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

	archiveData := createTestZipArchive(t)
	contentBuffer, contentType := createTestMultipartForm(t, archiveData)

	response := testUploadArtifacts(contentType, &contentBuffer, t, ts)
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
		time.Sleep(10 * time.Second)
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
			Timeout:  1,
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	archiveData := createTestZipArchive(t)
	contentBuffer, contentType := createTestMultipartForm(t, archiveData)

	response := testUploadArtifacts(contentType, &contentBuffer, t, ts)
	testhelper.AssertResponseCode(t, response, 500)
	assert.Equal(t, 1, putCalledTimes, "upload should be called only once")
}
