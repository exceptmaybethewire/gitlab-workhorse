package artifacts

import (
	"archive/zip"
	"bytes"
	"context"
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
	require.NoError(t, err)
	defer os.RemoveAll(tempPath)

	archiveData, md5 := createTestZipArchive(t)
	archiveFile, err := ioutil.TempFile("", "artifact.zip")
	require.NoError(t, err)
	defer os.Remove(archiveFile.Name())
	_, err = archiveFile.Write(archiveData)
	require.NoError(t, err)
	archiveFile.Close()

	tests := []struct {
		name    string
		preauth api.Response
	}{
		{
			name:    "ObjectStore Upload",
			preauth: api.Response{},
		},
		{
			name:    "ObjectStore and FileStore Upload",
			preauth: api.Response{TempPath: tempPath},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			osStub, testServer, err := test.StartObjectStore(ctx)
			require.NoError(t, err)

			objectPath := "/bucket/object"
			objectURL := testServer.URL + objectPath
			testCase.preauth.RemoteObject = api.RemoteObject{
				ID:       "object-id",
				GetURL:   objectURL,
				StoreURL: objectURL,
			}

			ts := testArtifactsUploadServer(t, testCase.preauth, nil)
			defer ts.Close()

			contentBuffer, contentType := createTestMultipartForm(t, archiveData)
			response := testUploadArtifacts(contentType, &contentBuffer, t, ts)
			testhelper.AssertResponseCode(t, response, 200)
			assert.Equal(t, md5, osStub.GetObjectMD5(objectPath))
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
