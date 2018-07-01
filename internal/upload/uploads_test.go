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
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

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

func (a *testFormProcessor) FormParams(formParams url.Values) error {
	return nil
}

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
	if err != nil {
		t.Fatal(err)
	}
	HandleFileUploads(response, request, nilHandler, &api.Response{}, nil)
	testhelper.AssertResponseCode(t, response, 500)
}

func TestUploadHandlerForwardingRawData(t *testing.T) {
	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`/url/path\z`), func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Fatal("Expected PATCH request")
		}

		var body bytes.Buffer
		io.Copy(&body, r.Body)
		if body.String() != "REQUEST" {
			t.Fatal("Expected REQUEST in request body")
		}

		w.WriteHeader(202)
		fmt.Fprint(w, "RESPONSE")
	})
	defer ts.Close()

	httpRequest, err := http.NewRequest("PATCH", ts.URL+"/url/path", bytes.NewBufferString("REQUEST"))
	if err != nil {
		t.Fatal(err)
	}

	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	response := httptest.NewRecorder()

	handler := newProxy(ts.URL)
	HandleFileUploads(response, httpRequest, handler, &api.Response{TempPath: tempPath}, nil)
	testhelper.AssertResponseCode(t, response, 202)
	if response.Body.String() != "RESPONSE" {
		t.Fatal("Expected RESPONSE in response body")
	}
}

func TestUploadHandlerRewritingMultiPartData(t *testing.T) {
	var filePath string

	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	ts := testhelper.TestServerWithHandler(regexp.MustCompile(`/url/path\z`), func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Fatal("Expected PUT request")
		}

		err := r.ParseMultipartForm(100000)
		if err != nil {
			t.Fatal(err)
		}

		if len(r.MultipartForm.File) != 0 {
			t.Error("Expected to not receive any files")
		}

		if r.FormValue("token") != "test" {
			t.Error("Expected to receive token")
		}

		if r.FormValue("file.name") != "my.file" {
			t.Error("Expected to receive a filename")
		}

		filePath = r.FormValue("file.path")
		if !strings.HasPrefix(filePath, tempPath) {
			t.Error("Expected to the file to be in tempPath")
		}

		if r.FormValue("file.size") != "4" {
			t.Error("Expected to receive the file size")
		}

		hashes := map[string]string{
			"md5":    "098f6bcd4621d373cade4e832627b4f6",
			"sha1":   "a94a8fe5ccb19ba61c4c0873d391e987982fbbd3",
			"sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			"sha512": "ee26b0dd4af7e749aa1a8ee3c10ae9923f618980772e473f8819a5d4940e0db27ac185f8a0e1d5f84f88bc887fd67b143732c304cc5fa9ad8e6f57f50028a8ff",
		}

		for algo, hash := range hashes {
			if r.FormValue("file."+algo) != hash {
				t.Errorf("Expected to receive file %s hash", algo)
			}
		}

		if valueCnt := len(r.MultipartForm.Value); valueCnt != 8 {
			t.Fatal("Expected to receive exactly 8 values but got", valueCnt)
		}

		w.WriteHeader(202)
		fmt.Fprint(w, "RESPONSE")
	})

	var buffer bytes.Buffer

	writer := multipart.NewWriter(&buffer)
	writer.WriteField("token", "test")
	file, err := writer.CreateFormFile("file", "my.file")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(file, "test")
	writer.Close()

	httpRequest, err := http.NewRequest("PUT", ts.URL+"/url/path", nil)
	if err != nil {
		t.Fatal(err)
	}

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

	if !os.IsNotExist(err) {
		t.Fatal("expected the file to be deleted")
	}
}

func TestUploadProcessingField(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	var buffer bytes.Buffer

	writer := multipart.NewWriter(&buffer)
	writer.WriteField("token2", "test")
	writer.Close()

	httpRequest, err := http.NewRequest("PUT", "/url/path", &buffer)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())

	response := httptest.NewRecorder()
	HandleFileUploads(response, httpRequest, nilHandler, &api.Response{TempPath: tempPath}, &testFormProcessor{})
	testhelper.AssertResponseCode(t, response, 500)
}

func TestUploadProcessingFile(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	_, testServer := test.StartObjectStore()
	defer testServer.Close()

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
			if err != nil {
				t.Fatal(err)
			}
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
	if err != nil {
		t.Fatal(err)
	}
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
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(file, "test")
		writer.Close()

		httpRequest, err := http.NewRequest("POST", "/example", buffer)
		if err != nil {
			t.Fatal(err)
		}
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
