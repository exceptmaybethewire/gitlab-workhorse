package artifacts

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
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
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/objectstore/test"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

func createTestZipArchive(t *testing.T) (data []byte, md5Hash string) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	fileInArchive, err := archive.Create("test.file")
	require.NoError(t, err)
	fmt.Fprint(fileInArchive, "test")
	archive.Close()
	data = buffer.Bytes()

	hasher := md5.New()
	hasher.Write(data)
	hexHash := hasher.Sum(nil)
	md5Hash = hex.EncodeToString(hexHash)

	return data, md5Hash
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

func testUploadArtifactsFromTestZip(t *testing.T, ts *httptest.Server) *httptest.ResponseRecorder {
	archiveData, _ := createTestZipArchive(t)
	contentBuffer, contentType := createTestMultipartForm(t, archiveData)

	return testUploadArtifacts(contentType, &contentBuffer, t, ts)
}

func TestUploadHandlerSendingToExternalStorage(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	archiveData, md5 := createTestZipArchive(t)
	archiveFile, err := ioutil.TempFile("", "artifact.zip")
	require.NoError(t, err)
	defer os.Remove(archiveFile.Name())
	_, err = archiveFile.Write(archiveData)
	require.NoError(t, err)
	archiveFile.Close()

	storeServerCalled := 0
	storeServerMux := http.NewServeMux()
	storeServerMux.HandleFunc("/url/put", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)

		receivedData, err := ioutil.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, archiveData, receivedData)

		storeServerCalled++
		w.Header().Set("ETag", md5)
		w.WriteHeader(200)
	})
	storeServerMux.HandleFunc("/store-id", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, archiveFile.Name())
	})

	responseProcessorCalled := 0
	responseProcessor := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "store-id", r.FormValue("file.remote_id"))
		assert.NotEmpty(t, r.FormValue("file.remote_url"))
		w.WriteHeader(200)
		responseProcessorCalled++
	}

	storeServer := httptest.NewServer(storeServerMux)
	defer storeServer.Close()

	tests := []struct {
		name    string
		preauth api.Response
	}{
		{
			name: "ObjectStore Upload",
			preauth: api.Response{
				RemoteObject: api.RemoteObject{
					StoreURL: storeServer.URL + "/url/put",
					ID:       "store-id",
					GetURL:   storeServer.URL + "/store-id",
				},
			},
		},
		{
			name: "ObjectStore and FileStore Upload",
			preauth: api.Response{
				TempPath: tempPath,
				RemoteObject: api.RemoteObject{
					StoreURL: storeServer.URL + "/url/put",
					ID:       "store-id",
					GetURL:   storeServer.URL + "/store-id",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storeServerCalled = 0
			responseProcessorCalled = 0

			ts := testArtifactsUploadServer(t, test.preauth, responseProcessor)
			defer ts.Close()

			contentBuffer, contentType := createTestMultipartForm(t, archiveData)
			response := testUploadArtifacts(contentType, &contentBuffer, t, ts)
			testhelper.AssertResponseCode(t, response, 200)
			assert.Equal(t, 1, storeServerCalled, "store should be called only once")
			assert.Equal(t, 1, responseProcessorCalled, "response processor should be called only once")
		})
	}
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
		RemoteObject: api.RemoteObject{
			StoreURL: "http://localhost:12323/invalid/url",
			ID:       "store-id",
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	response := testUploadArtifactsFromTestZip(t, ts)
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
		RemoteObject: api.RemoteObject{
			StoreURL: "htt:////invalid-url",
			ID:       "store-id",
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	response := testUploadArtifactsFromTestZip(t, ts)
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
		RemoteObject: api.RemoteObject{
			StoreURL: storeServer.URL + "/url/put",
			ID:       "store-id",
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	response := testUploadArtifactsFromTestZip(t, ts)
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
		RemoteObject: api.RemoteObject{
			StoreURL: storeServer.URL + "/url/put",
			ID:       "store-id",
			Timeout:  1,
		},
	}

	ts := testArtifactsUploadServer(t, authResponse, responseProcessor)
	defer ts.Close()

	response := testUploadArtifactsFromTestZip(t, ts)
	testhelper.AssertResponseCode(t, response, 500)
	assert.Equal(t, 1, putCalledTimes, "upload should be called only once")
}

func TestUploadHandlerMultipartUploadSizeLimit(t *testing.T) {
	os, server := test.StartObjectStore()
	defer server.Close()

	err := os.InitiateMultipartUpload(test.ObjectPath)
	require.NoError(t, err)

	objectURL := server.URL + test.ObjectPath

	partSize := int64(1)
	uploadSize := 10
	preauth := api.Response{
		RemoteObject: api.RemoteObject{
			ID: "store-id",
			MultipartUpload: &api.MultipartUploadParams{
				PartSize:    partSize,
				PartURLs:    []string{objectURL + "?partNumber=1"},
				AbortURL:    objectURL, // DELETE
				CompleteURL: objectURL, // POST
			},
		},
	}

	responseProcessor := func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("it should not be called")
	}

	ts := testArtifactsUploadServer(t, preauth, responseProcessor)
	defer ts.Close()

	contentBuffer, contentType := createTestMultipartForm(t, make([]byte, uploadSize))
	response := testUploadArtifacts(contentType, &contentBuffer, t, ts)
	testhelper.AssertResponseCode(t, response, http.StatusRequestEntityTooLarge)

	// Poll because AbortMultipartUpload is async
	for i := 0; os.IsMultipartUpload(test.ObjectPath) && i < 100; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	assert.False(t, os.IsMultipartUpload(test.ObjectPath), "MultipartUpload should not be in progress anymore")
	assert.Empty(t, os.GetObjectMD5(test.ObjectPath), "upload should have failed, so the object should not exists")
}
