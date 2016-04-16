package builds

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

func testRawTraceDownloadServer(t *testing.T, traceFile string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/url/path", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Fatal("Expected GET request")
		}

		w.Header().Set("Content-Type", "application/json")

		data, err := json.Marshal(&api.Response{
			TraceFile: traceFile,
		})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	})
	return testhelper.TestServerWithHandler(nil, mux.ServeHTTP)
}

func testDownloadRawTrace(t *testing.T, ts *httptest.Server) *httptest.ResponseRecorder {
	httpRequest, err := http.NewRequest("GET", ts.URL+"/url/path", nil)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	apiClient := api.NewAPI(helper.URLMustParse(ts.URL), "123", nil)
	RawTrace(apiClient).ServeHTTP(response, httpRequest)

	return response
}

func TestDownloadRawTrace(t *testing.T) {
	tempFile, err := ioutil.TempFile("", "build_trace")
	if err != nil {
		t.Fatal(err)
	}
	defer tempFile.Close()
	defer os.Remove(tempFile.Name())

	fmt.Fprint(tempFile, "BUILD TRACE")

	ts := testRawTraceDownloadServer(t, tempFile.Name())
	defer ts.Close()

	response := testDownloadRawTrace(t, ts)
	testhelper.AssertResponseCode(t, response, 200)

	testhelper.AssertResponseHeader(t, response,
		"Content-Type",
		"text/plain; charset=utf-8")

	testhelper.AssertResponseBody(t, response, "BUILD TRACE")
}

func TestRawTraceFromInvalidFile(t *testing.T) {
	ts := testRawTraceDownloadServer(t, "path/to/non/existing/file")
	defer ts.Close()

	response := testDownloadRawTrace(t, ts)
	testhelper.AssertResponseCode(t, response, 404)
}

func TestIncompleteApiResponse(t *testing.T) {
	ts := testRawTraceDownloadServer(t, "")
	defer ts.Close()

	response := testDownloadRawTrace(t, ts)
	testhelper.AssertResponseCode(t, response, 500)
}
