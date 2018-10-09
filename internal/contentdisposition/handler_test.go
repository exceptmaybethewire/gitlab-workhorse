package contentdisposition

import (
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandler(t *testing.T) {
	testCases := []struct {
		desc        string
		contentType string
		body        string
	}{
		{
			desc:        "do nothing",
			contentType: "text/plain",
			body:        "Hello world! \x41",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// We are pretending to be rails, or maybe object storage?
				w.Header().Set("Content-Type", tc.contentType)
				_, err := io.WriteString(w, tc.body)
				require.NoError(t, err)
			})

			req, err := http.NewRequest("GET", "/", nil)
			require.NoError(t, err)
			rw := httptest.NewRecorder()
			NewSWFBlocker(h).ServeHTTP(rw, req)

			resp := rw.Result()
			respBody, err := ioutil.ReadAll(resp.Body)
			require.NoError(t, err)

			require.Equal(t, tc.contentType, resp.Header.Get("Content-Type"))
			require.Equal(t, tc.body, string(respBody))
		})
	}
}
