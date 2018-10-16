package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/dgrijalva/jwt-go"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/badgateway"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/secret"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

func okHandler(w http.ResponseWriter, _ *http.Request, _ *api.Response) {
	w.WriteHeader(201)
	fmt.Fprint(w, "{\"status\":\"ok\"}")
}

func runPreAuthorizeHandler(t *testing.T, ts *httptest.Server, suffix string, url *regexp.Regexp, apiResponse interface{}, returnCode, expectedCode int) *httptest.ResponseRecorder {
	if ts == nil {
		ts = testAuthServer(url, returnCode, apiResponse)
		defer ts.Close()
	}

	// Create http request
	httpRequest, err := http.NewRequest("GET", "/address", nil)
	if err != nil {
		t.Fatal(err)
	}
	parsedURL := helper.URLMustParse(ts.URL)
	testhelper.ConfigureSecret()
	a := api.NewAPI(parsedURL, "123", badgateway.TestRoundTripper(parsedURL))

	response := httptest.NewRecorder()
	a.PreAuthorizeHandler(okHandler, suffix).ServeHTTP(response, httpRequest)
	testhelper.AssertResponseCode(t, response, expectedCode)
	return response
}

func TestPreAuthorizeHappyPath(t *testing.T) {
	runPreAuthorizeHandler(
		t, nil, "/authorize",
		regexp.MustCompile(`/authorize\z`),
		&api.Response{},
		200, 201)
}

func TestPreAuthorizeSuffix(t *testing.T) {
	runPreAuthorizeHandler(
		t, nil, "/different-authorize",
		regexp.MustCompile(`/authorize\z`),
		&api.Response{},
		200, 404)
}

func TestPreAuthorizeJsonFailure(t *testing.T) {
	runPreAuthorizeHandler(
		t, nil, "/authorize",
		regexp.MustCompile(`/authorize\z`),
		"not-json",
		200, 500)
}

func TestPreAuthorizeContentTypeFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`{"hello":"world"}`)); err != nil {
			t.Fatalf("write auth response: %v", err)
		}
	}))
	defer ts.Close()

	runPreAuthorizeHandler(
		t, ts, "/authorize",
		regexp.MustCompile(`/authorize\z`),
		"",
		200, 200)
}

func TestPreAuthorizeRedirect(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", 301)
	}))
	defer ts.Close()

	runPreAuthorizeHandler(t, ts, "/willredirect",
		regexp.MustCompile(`/willredirect\z`),
		"",
		301, 301)
}

func TestPreAuthorizeJWT(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := jwt.Parse(r.Header.Get(api.RequestHeader), func(token *jwt.Token) (interface{}, error) {
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
			t.Fatalf("decode token: %v", err)
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			t.Fatal("claims cast failed")
		}

		if !token.Valid {
			t.Fatal("JWT token invalid")
		}

		if claims["iss"] != "gitlab-workhorse" {
			t.Fatalf("execpted issuer gitlab-workhorse, got %q", claims["iss"])
		}

		w.Header().Set("Content-Type", api.ResponseContentType)
		if _, err := w.Write([]byte(`{"hello":"world"}`)); err != nil {
			t.Fatalf("write auth response: %v", err)
		}
	}))
	defer ts.Close()

	runPreAuthorizeHandler(
		t, ts, "/authorize",
		regexp.MustCompile(`/authorize\z`),
		"",
		200, 201)
}
