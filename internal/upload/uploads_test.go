package upload

import (
	"bytes"
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

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/badgateway"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/proxy"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

var nilHandler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

type testFormProcessor struct {
}

func (a *testFormProcessor) ProcessFile(formName, fileName string, writer *multipart.Writer) error {
	if formName != "file" && fileName != "my.file" {
		return errors.New("illegal file")
	}
	return nil
}

func (a *testFormProcessor) ProcessField(formName string, writer *multipart.Writer) error {
	if formName != "token" {
		return errors.New("illegal field")
	}
	return nil
}

func TestUploadTempPathRequirement(t *testing.T) {
	response := httptest.NewRecorder()
	request, err := http.NewRequest("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	HandleFileUploads(response, request, nilHandler, "", nil)
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
	HandleFileUploads(response, httpRequest, handler, tempPath, nil)
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

		if len(r.MultipartForm.Value) != 3 {
			t.Fatal("Expected to receive exactly 3 values")
		}

		if len(r.MultipartForm.File) != 0 {
			t.Fatal("Expected to not receive any files")
		}

		if r.FormValue("token") != "test" {
			t.Fatal("Expected to receive token")
		}

		if r.FormValue("file.name") != "my.file" {
			t.Fatal("Expected to receive a filename")
		}

		filePath = r.FormValue("file.path")

		if !strings.HasPrefix(r.FormValue("file.path"), tempPath) {
			t.Fatal("Expected to the file to be in tempPath")
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

	httpRequest.Body = ioutil.NopCloser(&buffer)
	httpRequest.ContentLength = int64(buffer.Len())
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()

	handler := newProxy(ts.URL)
	HandleFileUploads(response, httpRequest, handler, tempPath, &testFormProcessor{})
	testhelper.AssertResponseCode(t, response, 202)

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
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
	HandleFileUploads(response, httpRequest, nilHandler, tempPath, &testFormProcessor{})
	testhelper.AssertResponseCode(t, response, 500)
}

func TestUploadProcessingFile(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	var buffer bytes.Buffer

	writer := multipart.NewWriter(&buffer)
	file, err := writer.CreateFormFile("file2", "my.file")
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
	HandleFileUploads(response, httpRequest, nilHandler, tempPath, &testFormProcessor{})
	testhelper.AssertResponseCode(t, response, 500)
}

func newProxy(url string) *proxy.Proxy {
	parsedURL := helper.URLMustParse(url)
	return proxy.NewProxy(parsedURL, "123", badgateway.TestRoundTripper(parsedURL))
}
