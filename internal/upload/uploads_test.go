package upload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/badgateway"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/objectstore/test"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/proxy"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

var nilHandler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

type testFormProcessor struct{}

func (a *testFormProcessor) ProcessFile(ctx context.Context, formName string, file *filestore.FileHandler, writer *multipart.Writer) error {
	return nil
}

func (a *testFormProcessor) ProcessField(ctx context.Context, formName string, writer *multipart.Writer) error {
	if formName != "token" {
		return errors.New("illegal field")
	}
	return nil
}

func (a *testFormProcessor) Finalize(ctx context.Context) error {
	return nil
}

func (a *testFormProcessor) Name() string {
	return ""
}

func TestUploadTempPathRequirement(t *testing.T) {
	response := httptest.NewRecorder()
	request, err := http.NewRequest("", "", nil)
	require.NoError(t, err)

	HandleFileUploads(response, request, nilHandler, &api.Response{}, nil)
	testhelper.AssertResponseCode(t, response, 500)
}

func TestUploadHandlerForwardingRawData(t *testing.T) {
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`/url/path\z`), func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PATCH", r.Method, "Expected PATCH request")

		var body bytes.Buffer
		io.Copy(&body, r.Body)
		require.Equal(t, "REQUEST", body.String(), "Expected REQUEST in request body")

		w.WriteHeader(202)
		fmt.Fprint(w, "RESPONSE")
	})
	defer ts.Close()

	httpRequest, err := http.NewRequest("PATCH", ts.URL+"/url/path", bytes.NewBufferString("REQUEST"))
	require.NoError(t, err)

	tempPath, err := ioutil.TempDir("", "uploads")
	require.NoError(t, err)
	defer os.RemoveAll(tempPath)

	response := httptest.NewRecorder()

	handler := newProxy(ts.URL)
	HandleFileUploads(response, httpRequest, handler, &api.Response{TempPath: tempPath}, nil)
	testhelper.AssertResponseCode(t, response, 202)
	assert.Equal(t, "RESPONSE", response.Body.String(), "Expected RESPONSE in response body")
}

func TestUploadHandlerRewritingMultiPartData(t *testing.T) {
	var filePath string

	tempPath, err := ioutil.TempDir("", "uploads")
	require.NoError(t, err)
	defer os.RemoveAll(tempPath)

	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`/url/path\z`), func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PUT", r.Method, "Expected PUT request")

		err := r.ParseMultipartForm(100000)
		require.NoError(t, err)

		assert.Empty(t, r.MultipartForm.File, "No file expected")

		assert.Equal(t, "test", r.FormValue("token"), "Expected to receive a token")
		assert.Equal(t, "my.file", r.FormValue("file.name"), "Expected to receive a filename")

		filePath = r.FormValue("file.path")
		assert.True(t, strings.HasPrefix(filePath, tempPath), "Expected to the file to be in tempPath")

		assert.Equal(t, "4", r.FormValue("file.size"), "Expected to receive the file size")

		hashes := map[string]string{
			"md5":    "098f6bcd4621d373cade4e832627b4f6",
			"sha1":   "a94a8fe5ccb19ba61c4c0873d391e987982fbbd3",
			"sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			"sha512": "ee26b0dd4af7e749aa1a8ee3c10ae9923f618980772e473f8819a5d4940e0db27ac185f8a0e1d5f84f88bc887fd67b143732c304cc5fa9ad8e6f57f50028a8ff",
		}

		for algo, hash := range hashes {
			assert.Equal(t, hash, r.FormValue("file."+algo), fmt.Sprintf("Wrong %s hash", algo))
		}

		assert.Len(t, r.MultipartForm.Value, 8, "Wrong number of MultipartForm values")

		w.WriteHeader(202)
		fmt.Fprint(w, "RESPONSE")
	})

	var buffer bytes.Buffer

	writer := multipart.NewWriter(&buffer)
	writer.WriteField("token", "test")
	file, err := writer.CreateFormFile("file", "my.file")
	require.NoError(t, err)
	fmt.Fprint(file, "test")
	writer.Close()

	httpRequest, err := http.NewRequest("PUT", ts.URL+"/url/path", nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	httpRequest = httpRequest.WithContext(ctx)
	httpRequest.Body = ioutil.NopCloser(&buffer)
	httpRequest.ContentLength = int64(buffer.Len())
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()

	handler := newProxy(ts.URL)
	HandleFileUploads(response, httpRequest, handler, &api.Response{TempPath: tempPath}, &testFormProcessor{})
	testhelper.AssertResponseCode(t, response, 202)

	cancel() // this will trigger an async cleanup

	// Poll because the file removal is async
	for i := 0; i < 100; i++ {
		_, err = os.Stat(filePath)
		if err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	assert.True(t, os.IsNotExist(err), "expected the file to be deleted")
}

func TestUploadProcessingField(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	require.NoError(t, err)
	defer os.RemoveAll(tempPath)

	var buffer bytes.Buffer

	writer := multipart.NewWriter(&buffer)
	writer.WriteField("token2", "test")
	writer.Close()

	httpRequest, err := http.NewRequest("PUT", "/url/path", &buffer)
	require.NoError(t, err)
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())

	response := httptest.NewRecorder()
	HandleFileUploads(response, httpRequest, nilHandler, &api.Response{TempPath: tempPath}, &testFormProcessor{})
	testhelper.AssertResponseCode(t, response, 500)
}

func TestUploadProcessingFile(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	require.NoError(t, err)
	defer os.RemoveAll(tempPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, testServer, err := test.StartObjectStore(ctx)
	require.NoError(t, err)

	storeUrl := testServer.URL + test.ObjectPath

	tests := []struct {
		name    string
		preauth api.Response
	}{
		{
			name:    "FileStore Upload",
			preauth: api.Response{TempPath: tempPath},
		},
		{
			name:    "ObjectStore Upload",
			preauth: api.Response{RemoteObject: api.RemoteObject{StoreURL: storeUrl}},
		},
		{
			name: "ObjectStore and FileStore Upload",
			preauth: api.Response{
				TempPath:     tempPath,
				RemoteObject: api.RemoteObject{StoreURL: storeUrl},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			writer := multipart.NewWriter(&buffer)
			file, err := writer.CreateFormFile("file", "my.file")
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprint(file, "test")
			writer.Close()

			httpRequest, err := http.NewRequest("PUT", "/url/path", &buffer)
			require.NoError(t, err)

			httpRequest.Header.Set("Content-Type", writer.FormDataContentType())

			response := httptest.NewRecorder()
			HandleFileUploads(response, httpRequest, nilHandler, &test.preauth, &testFormProcessor{})
			testhelper.AssertResponseCode(t, response, 200)
		})
	}

}

func TestInvalidFileNames(t *testing.T) {
	testhelper.ConfigureSecret()

	tempPath, err := ioutil.TempDir("", "uploads")
	require.NoError(t, err)
	defer os.RemoveAll(tempPath)

	for _, testCase := range []struct {
		filename string
		code     int
	}{
		{"foobar", 200}, // sanity check for test setup below
		{"foo/bar", 500},
		{"/../../foobar", 500},
		{".", 500},
		{"..", 500},
	} {
		buffer := &bytes.Buffer{}

		writer := multipart.NewWriter(buffer)
		file, err := writer.CreateFormFile("file", testCase.filename)
		require.NoError(t, err)
		fmt.Fprint(file, "test")
		writer.Close()

		httpRequest, err := http.NewRequest("POST", "/example", buffer)
		require.NoError(t, err)
		httpRequest.Header.Set("Content-Type", writer.FormDataContentType())

		response := httptest.NewRecorder()
		HandleFileUploads(response, httpRequest, nilHandler, &api.Response{TempPath: tempPath}, &savedFileTracker{request: httpRequest})
		testhelper.AssertResponseCode(t, response, testCase.code)
	}
}

func newProxy(url string) *proxy.Proxy {
	parsedURL := helper.URLMustParse(url)
	return proxy.NewProxy(parsedURL, "123", badgateway.TestRoundTripper(parsedURL))
}
