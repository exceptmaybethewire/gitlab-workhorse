package artifacts

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/badgateway"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/proxy"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/zipartifacts"
)

func testArtifactsUploadServer(t *testing.T, authResponse api.Response, bodyProcessor func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/url/path/authorize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatal("Expected POST request")
		}

		w.Header().Set("Content-Type", api.ResponseContentType)

		data, err := json.Marshal(&authResponse)
		if err != nil {
			t.Fatal("Expected to marshal")
		}
		w.Write(data)
	})
	mux.HandleFunc("/url/path", func(w http.ResponseWriter, r *http.Request) {
		opts := filestore.GetOpts(&authResponse)
		artifactFormat := r.FormValue("artifact_format")
		artifactType := r.FormValue("artifact_type")

		if r.Method != "POST" {
			t.Fatal("Expected POST request")
		}
		if opts.IsLocal() {
			if r.FormValue("file.path") == "" {
				w.WriteHeader(501)
				return
			}

			_, err := ioutil.ReadFile(r.FormValue("file.path"))
			if err != nil {
				w.WriteHeader(404)
				return
			}
		}

		if opts.IsRemote() && r.FormValue("file.remote_url") == "" {
			w.WriteHeader(501)
			return
		}

		if artifactFormat != "" {
			if artifactFormat != "zip" && artifactFormat != "gzip" {
				w.WriteHeader(400)
				return
			}
		}

		if artifactType != "" {
			if artifactType != "archive" && artifactType != "junit" {
				w.WriteHeader(400)
				return
			}
		}

		if artifactFormat == "" || artifactFormat == "zip" {
			if r.FormValue("metadata.path") == "" {
				w.WriteHeader(502)
				return
			}

			metadata, err := ioutil.ReadFile(r.FormValue("metadata.path"))
			if err != nil {
				w.WriteHeader(404)
				return
			}
			gz, err := gzip.NewReader(bytes.NewReader(metadata))
			if err != nil {
				w.WriteHeader(405)
				return
			}
			defer gz.Close()
			metadata, err = ioutil.ReadAll(gz)
			if err != nil {
				w.WriteHeader(404)
				return
			}
			if !bytes.HasPrefix(metadata, []byte(zipartifacts.MetadataHeaderPrefix+zipartifacts.MetadataHeader)) {
				w.WriteHeader(400)
				return
			}
		}

		if bodyProcessor != nil {
			bodyProcessor(w, r)
		} else {
			w.WriteHeader(200)
		}
	})
	return testhelper.TestServerWithHandler(nil, mux.ServeHTTP)
}

func testUploadArtifacts(query url.Values, contentType string, body io.Reader, t *testing.T, ts *httptest.Server) *httptest.ResponseRecorder {
	httpRequest, err := http.NewRequest("POST", fmt.Sprintf("%s/url/path?%s", ts.URL, query.Encode()), body)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	parsedURL := helper.URLMustParse(ts.URL)
	roundTripper := badgateway.TestRoundTripper(parsedURL)
	testhelper.ConfigureSecret()
	apiClient := api.NewAPI(parsedURL, "123", roundTripper)
	proxyClient := proxy.NewProxy(parsedURL, "123", roundTripper)
	UploadArtifacts(apiClient, proxyClient).ServeHTTP(response, httpRequest)
	return response
}

func TestUploadHandlerAddingMetadata(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	ts := testArtifactsUploadServer(t, api.Response{TempPath: tempPath}, nil)
	defer ts.Close()

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	file, err := writer.CreateFormFile("file", "my.file")
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	defer archive.Close()

	fileInArchive, err := archive.Create("test.file")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(fileInArchive, "test")
	archive.Close()
	writer.Close()

	response := testUploadArtifacts(url.Values{}, writer.FormDataContentType(), &buffer, t, ts)
	testhelper.AssertResponseCode(t, response, 200)
}

func TestUploadHandlerForUnsupportedArchive(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	ts := testArtifactsUploadServer(t, api.Response{TempPath: tempPath}, nil)
	defer ts.Close()

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	file, err := writer.CreateFormFile("file", "my.file")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(file, "test")
	writer.Close()

	response := testUploadArtifacts(url.Values{}, writer.FormDataContentType(), &buffer, t, ts)
	// 502 is a custom response code from the mock server in testUploadArtifacts
	testhelper.AssertResponseCode(t, response, 502)
}

func TestUploadFormProcessing(t *testing.T) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	ts := testArtifactsUploadServer(t, api.Response{TempPath: tempPath}, nil)
	defer ts.Close()

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	file, err := writer.CreateFormFile("metadata", "my.file")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(file, "test")
	writer.Close()

	response := testUploadArtifacts(url.Values{}, writer.FormDataContentType(), &buffer, t, ts)
	testhelper.AssertResponseCode(t, response, 500)
}

func testRawGzipArtifactRequest(t *testing.T) (string, *bytes.Buffer, *httptest.Server) {
	tempPath, err := ioutil.TempDir("", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)

	ts := testArtifactsUploadServer(t, api.Response{TempPath: tempPath}, nil)

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	file, err := writer.CreateFormFile("file", "my.file")
	if err != nil {
		t.Fatal(err)
	}
	rawGzip := gzip.NewWriter(file)
	defer rawGzip.Close()

	rawGzip.Write([]byte("<testsuites>junit.xml</testsuites>"))
	rawGzip.Close()
	writer.Close()

	return writer.FormDataContentType(), &buffer, ts
}

func TestUploadHandlerWithGZipFormat(t *testing.T) {
	contentType, buffer, ts := testRawGzipArtifactRequest(t)
	defer ts.Close()

	query := url.Values{}
	query.Set("artifact_format", "gzip")
	query.Set("artifact_type", "junit")

	response := testUploadArtifacts(query, contentType, buffer, t, ts)
	testhelper.AssertResponseCode(t, response, 200)
}

func TestUploadHandlerWithGZipFormatButWrongParameter(t *testing.T) {
	contentType, buffer, ts := testRawGzipArtifactRequest(t)
	defer ts.Close()

	query := url.Values{}
	query.Set("artifact_format", "zip")
	query.Set("artifact_type", "junit")

	response := testUploadArtifacts(query, contentType, buffer, t, ts)
	testhelper.AssertResponseCode(t, response, 502) // gitlab-zip-metadata: not a zip
}
