package sendurl

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

const testData = `123456789012345678901234567890`
const testDataEtag = `W/"myetag"`

func testEntryServer(t *testing.T, requestURL string, httpHeaders http.Header, allowRedirects bool) *httptest.ResponseRecorder {
	requestHandler := func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)

		url := r.URL.String() + "/file"
		jsonParams := fmt.Sprintf(`{"URL":%q,"AllowRedirects":%s}`,
			url, strconv.FormatBool(allowRedirects))
		data := base64.URLEncoding.EncodeToString([]byte(jsonParams))

		// The server returns a Content-Disposition
		w.Header().Set("Content-Disposition", "attachment; filename=\"archive.txt\"")

		SendURL.Inject(w, r, data)
	}
	serveFile := func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)

		tempFile, err := ioutil.TempFile("", "download_file")
		require.NoError(t, err)
		require.NoError(t, os.Remove(tempFile.Name()))
		defer tempFile.Close()
		_, err = tempFile.Write([]byte(testData))
		require.NoError(t, err)

		w.Header().Set("Etag", testDataEtag)

		http.ServeContent(w, r, "archive.txt", time.Now(), tempFile)
	}
	redirectFile := func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		http.Redirect(w, r, r.URL.String()+"/download", http.StatusTemporaryRedirect)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/get/request", requestHandler)
	mux.HandleFunc("/get/request/file", serveFile)
	mux.HandleFunc("/get/redirect", requestHandler)
	mux.HandleFunc("/get/redirect/file", redirectFile)
	mux.HandleFunc("/get/redirect/file/download", serveFile)
	mux.HandleFunc("/get/file-not-existing", requestHandler)

	server := httptest.NewServer(mux)
	defer server.Close()

	httpRequest, err := http.NewRequest("GET", server.URL+requestURL, nil)
	require.NoError(t, err)
	if httpHeaders != nil {
		httpRequest.Header = httpHeaders
	}

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httpRequest)
	return response
}

func TestDownloadingUsingSendURL(t *testing.T) {
	response := testEntryServer(t, "/get/request", nil, false)
	testhelper.AssertResponseCode(t, response, http.StatusOK)

	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Type",
		"text/plain; charset=utf-8")
	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Disposition",
		"attachment; filename=\"archive.txt\"")

	testhelper.AssertResponseBody(t, response, testData)
}

func TestDownloadingAChunkOfDataWithSendURL(t *testing.T) {
	httpHeaders := http.Header{
		"Range": []string{
			"bytes=1-2",
		},
	}

	response := testEntryServer(t, "/get/request", httpHeaders, false)
	testhelper.AssertResponseCode(t, response, http.StatusPartialContent)

	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Type",
		"text/plain; charset=utf-8")
	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Disposition",
		"attachment; filename=\"archive.txt\"")
	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Range",
		"bytes 1-2/30")

	testhelper.AssertResponseBody(t, response, "23")
}

func TestAccessingAlreadyDownloadedFileWithSendURL(t *testing.T) {
	httpHeaders := http.Header{
		"If-None-Match": []string{testDataEtag},
	}

	response := testEntryServer(t, "/get/request", httpHeaders, false)
	testhelper.AssertResponseCode(t, response, http.StatusNotModified)
}

func TestAccessingRedirectWithSendURL(t *testing.T) {
	response := testEntryServer(t, "/get/redirect", nil, false)
	testhelper.AssertResponseCode(t, response, http.StatusTemporaryRedirect)
}

func TestAccessingAllowedRedirectWithSendURL(t *testing.T) {
	response := testEntryServer(t, "/get/redirect", nil, true)
	testhelper.AssertResponseCode(t, response, http.StatusOK)

	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Type",
		"text/plain; charset=utf-8")
	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Disposition",
		"attachment; filename=\"archive.txt\"")
}

func TestAccessingAllowedRedirectWithChunkOfDataWithSendURL(t *testing.T) {
	httpHeaders := http.Header{
		"Range": []string{
			"bytes=1-2",
		},
	}

	response := testEntryServer(t, "/get/redirect", httpHeaders, true)
	testhelper.AssertResponseCode(t, response, http.StatusPartialContent)

	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Type",
		"text/plain; charset=utf-8")
	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Disposition",
		"attachment; filename=\"archive.txt\"")
	testhelper.AssertResponseWriterHeader(t, response,
		"Content-Range",
		"bytes 1-2/30")

	testhelper.AssertResponseBody(t, response, "23")
}

func TestDownloadingNonExistingFileUsingSendURL(t *testing.T) {
	response := testEntryServer(t, "/invalid/path", nil, false)
	testhelper.AssertResponseCode(t, response, http.StatusNotFound)
}

func TestDownloadingNonExistingRemoteFileWithSendURL(t *testing.T) {
	response := testEntryServer(t, "/get/file-not-existing", nil, false)
	testhelper.AssertResponseCode(t, response, http.StatusNotFound)
}
