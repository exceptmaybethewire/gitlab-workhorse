package helper

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetForwardedForGeneratesHeader(t *testing.T) {
	testCases := []struct {
		remoteAddr           string
		previousForwardedFor []string
		expected             string
	}{
		{
			"8.8.8.8:3000",
			nil,
			"8.8.8.8",
		},
		{
			"8.8.8.8:3000",
			[]string{"138.124.33.63, 151.146.211.237"},
			"138.124.33.63, 151.146.211.237, 8.8.8.8",
		},
		{
			"8.8.8.8:3000",
			[]string{"8.154.76.107", "115.206.118.179"},
			"8.154.76.107, 115.206.118.179, 8.8.8.8",
		},
	}
	for _, tc := range testCases {
		headers := http.Header{}
		originalRequest := http.Request{
			RemoteAddr: tc.remoteAddr,
		}

		if tc.previousForwardedFor != nil {
			originalRequest.Header = http.Header{
				"X-Forwarded-For": tc.previousForwardedFor,
			}
		}

		SetForwardedFor(&headers, &originalRequest)

		result := headers.Get("X-Forwarded-For")
		if result != tc.expected {
			t.Fatalf("Expected %v, got %v", tc.expected, result)
		}
	}
}

func TestReadRequestBody(t *testing.T) {
	data := []byte("123456")
	rw := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", bytes.NewBuffer(data))

	result, err := ReadRequestBody(rw, req, 1000)
	assert.NoError(t, err)
	assert.Equal(t, data, result)
}

func TestReadRequestBodyLimit(t *testing.T) {
	data := []byte("123456")
	rw := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", bytes.NewBuffer(data))

	_, err := ReadRequestBody(rw, req, 2)
	assert.Error(t, err)
}

func TestCloneRequestWithBody(t *testing.T) {
	input := []byte("test")
	newInput := []byte("new body")
	req, _ := http.NewRequest("POST", "/test", bytes.NewBuffer(input))
	newReq := CloneRequestWithNewBody(req, newInput)

	assert.NotEqual(t, req, newReq)
	assert.NotEqual(t, req.Body, newReq.Body)
	assert.NotEqual(t, len(newInput), newReq.ContentLength)

	var buffer bytes.Buffer
	io.Copy(&buffer, newReq.Body)
	assert.Equal(t, newInput, buffer.Bytes())
}

func TestApplicationJson(t *testing.T) {
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Header.Set("Content-Type", "application/json")

	assert.True(t, IsApplicationJson(req), "expected to match 'application/json' as 'application/json'")

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	assert.True(t, IsApplicationJson(req), "expected to match 'application/json; charset=utf-8' as 'application/json'")

	req.Header.Set("Content-Type", "text/plain")
	assert.False(t, IsApplicationJson(req), "expected not to match 'text/plain' as 'application/json'")
}

func TestFail500WorksWithNils(t *testing.T) {
	body := bytes.NewBuffer(nil)
	w := httptest.NewRecorder()
	w.Body = body

	Fail500(w, nil, nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "Internal server error\n", body.String())
}

func TestScrubURLParams(t *testing.T) {
	for before, expected := range map[string]string{
		"http://example.com":                                                "http://example.com",
		"http://example.com?foo=1":                                          "http://example.com?foo=1",
		"http://example.com?authenticity_token=1":                           "http://example.com?authenticity_token=[FILTERED]",
		"http://example.com?private_token=1":                                "http://example.com?private_token=[FILTERED]",
		"http://example.com?rss_token=1":                                    "http://example.com?rss_token=[FILTERED]",
		"http://example.com?foo&authenticity_token=blahblah&bar":            "http://example.com?foo&authenticity_token=[FILTERED]&bar",
		"http://example.com?private-token=1":                                "http://example.com?private-token=[FILTERED]",
		"http://example.com?foo&private-token=blahblah&bar":                 "http://example.com?foo&private-token=[FILTERED]&bar",
		"http://example.com?private-token=foo&authenticity_token=bar":       "http://example.com?private-token=[FILTERED]&authenticity_token=[FILTERED]",
		"https://example.com:8080?private-token=foo&authenticity_token=bar": "https://example.com:8080?private-token=[FILTERED]&authenticity_token=[FILTERED]",
		"/?private-token=foo&authenticity_token=bar":                        "/?private-token=[FILTERED]&authenticity_token=[FILTERED]",
		"?private-token=&authenticity_token=&bar":                           "?private-token=[FILTERED]&authenticity_token=[FILTERED]&bar",
		"?private-token=foo&authenticity_token=bar":                         "?private-token=[FILTERED]&authenticity_token=[FILTERED]",
		"?private_token=foo&authenticity-token=bar":                         "?private_token=[FILTERED]&authenticity-token=[FILTERED]",
		"?X-AMZ-Signature=foo":                                              "?X-AMZ-Signature=[FILTERED]",
		"&X-AMZ-Signature=foo":                                              "&X-AMZ-Signature=[FILTERED]",
		"?x-amz-signature=foo":                                              "?x-amz-signature=[FILTERED]",
		"&Signature=foo":                                                    "&Signature=[FILTERED]",
		"?Signature=foo":                                                    "?Signature=[FILTERED]",
	} {
		after := ScrubURLParams(before)
		assert.Equal(t, expected, after, "Scrubbing %q", before)
	}
}
