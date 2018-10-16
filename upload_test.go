package main

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/dgrijalva/jwt-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/secret"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/upload"
)

type uploadArtifactsFunction func(url, contentType string, body io.Reader) (*http.Response, string, error)

func uploadArtifactsV1(url, contentType string, body io.Reader) (*http.Response, string, error) {
	resource := `/ci/api/v1/builds/123/artifacts`
	resp, err := http.Post(url+resource, contentType, body)
	return resp, resource, err
}

func uploadArtifactsV4(url, contentType string, body io.Reader) (*http.Response, string, error) {
	resource := `/api/v4/jobs/123/artifacts`
	resp, err := http.Post(url+resource, contentType, body)
	return resp, resource, err
}

func testArtifactsUpload(t *testing.T, uploadArtifacts uploadArtifactsFunction) {
	reqBody, contentType, err := multipartBodyWithFile()
	require.NoError(t, err)

	ts := uploadTestServer(t, nil)
	defer ts.Close()

	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resp, resource, err := uploadArtifacts(ws.URL, contentType, reqBody)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode, "GET %q: expected 200, got %d", resource, resp.StatusCode)
}

func TestArtifactsUpload(t *testing.T) {
	testArtifactsUpload(t, uploadArtifactsV1)
	testArtifactsUpload(t, uploadArtifactsV4)
}

func uploadTestServer(t *testing.T, extraTests func(r *http.Request)) *httptest.Server {
	return testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/authorize") {
			w.Header().Set("Content-Type", api.ResponseContentType)
			if _, err := fmt.Fprintf(w, `{"TempPath":"%s"}`, scratchDir); err != nil {
				t.Fatal(err)
			}
			return
		}
		err := r.ParseMultipartForm(100000)
		if err != nil {
			t.Fatal(err)
		}
		nValues := 7 // file name, path, size, md5, sha1, sha256, sha512 for just the upload (no metadata because we are not POSTing a valid zip file)
		if len(r.MultipartForm.Value) != nValues {
			t.Errorf("Expected to receive exactly %d values", nValues)
		}
		if len(r.MultipartForm.File) != 0 {
			t.Error("Expected to not receive any files")
		}
		if extraTests != nil {
			extraTests(r)
		}
		w.WriteHeader(200)
	})
}

func TestAcceleratedUpload(t *testing.T) {
	reqBody, contentType, err := multipartBodyWithFile()
	if err != nil {
		t.Fatal(err)
	}
	ts := uploadTestServer(t, func(r *http.Request) {
		jwtToken, err := jwt.Parse(r.Header.Get(upload.RewrittenFieldsHeader), func(token *jwt.Token) (interface{}, error) {
			// Don't forget to validate the alg is what you expect:
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
			}
			testhelper.ConfigureSecret()
			secretBytes, err := secret.Bytes()
			if err != nil {
				return nil, fmt.Errorf("read secret from file: %v", err)
			}

			return secretBytes, nil
		})
		if err != nil {
			t.Fatal(err)
		}

		rewrittenFields := jwtToken.Claims.(jwt.MapClaims)["rewritten_fields"].(map[string]interface{})
		if len(rewrittenFields) != 1 || len(rewrittenFields["file"].(string)) == 0 {
			t.Fatalf("Unexpected rewritten_fields value: %v", rewrittenFields)
		}

	})

	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	resource := `/example`
	resp, err := http.Post(ws.URL+resource, contentType, reqBody)
	if err != nil {
		t.Error(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET %q: expected 200, got %d", resource, resp.StatusCode)
	}
}

func multipartBodyWithFile() (io.Reader, string, error) {
	result := &bytes.Buffer{}
	writer := multipart.NewWriter(result)
	file, err := writer.CreateFormFile("file", "my.file")
	if err != nil {
		return nil, "", err
	}
	fmt.Fprint(file, "SHOULD BE ON DISK, NOT IN MULTIPART")
	return result, writer.FormDataContentType(), writer.Close()
}

func TestBlockingRewrittenFieldsHeader(t *testing.T) {
	canary := "untrusted header passed by user"
	testCases := []struct {
		desc        string
		contentType string
		body        io.Reader
		present     bool
	}{
		{"multipart with file", "", nil, true}, // placeholder
		{"no multipart", "text/plain", nil, false},
	}

	if b, c, err := multipartBodyWithFile(); err == nil {
		testCases[0].contentType = c
		testCases[0].body = b
	} else {
		t.Fatal(err)
	}

	for _, tc := range testCases {
		ts := testhelper.TestServerWithHandler(regexp.MustCompile(`.`), func(w http.ResponseWriter, r *http.Request) {
			h := upload.RewrittenFieldsHeader
			if _, ok := r.Header[h]; ok != tc.present {
				t.Errorf("Expectation of presence (%v) violated", tc.present)
			}
			if r.Header.Get(h) == canary {
				t.Errorf("Found canary %q in header %q", canary, h)
			}
		})
		defer ts.Close()
		ws := startWorkhorseServer(ts.URL)
		defer ws.Close()

		req, err := http.NewRequest("POST", ws.URL+"/something", tc.body)
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("Content-Type", tc.contentType)
		req.Header.Set(upload.RewrittenFieldsHeader, canary)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: expected HTTP 200, got %d", tc.desc, resp.StatusCode)
		}

	}
}
