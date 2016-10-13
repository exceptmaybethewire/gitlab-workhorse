package api

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/badgateway"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"

	"github.com/dgrijalva/jwt-go"
)

// Custom content type for API responses, to catch routing / programming mistakes
const ResponseContentType = "application/vnd.gitlab-workhorse+json"

const RequestHeader = "Gitlab-Workhorse-Api-Request"

type API struct {
	Client  *http.Client
	URL     *url.URL
	Version string
	Secret  *Secret
}

func NewAPI(myURL *url.URL, version, secretPath string, roundTripper *badgateway.RoundTripper) *API {
	return &API{
		Client:  &http.Client{Transport: roundTripper},
		URL:     myURL,
		Version: version,
		Secret:  &Secret{Path: secretPath},
	}
}

type HandleFunc func(http.ResponseWriter, *http.Request, *Response)

type Response struct {
	// GL_ID is an environment variable used by gitlab-shell hooks during 'git
	// push' and 'git pull'
	GL_ID string
	// RepoPath is the full path on disk to the Git repository the request is
	// about
	RepoPath string
	// StoreLFSPath is provided by the GitLab Rails application
	// to mark where the tmp file should be placed
	StoreLFSPath string
	// LFS object id
	LfsOid string
	// LFS object size
	LfsSize int64
	// TmpPath is the path where we should store temporary files
	// This is set by authorization middleware
	TempPath string
	// Archive is the path where the artifacts archive is stored
	Archive string `json:"archive"`
	// Entry is a filename inside the archive point to file that needs to be extracted
	Entry string `json:"entry"`
}

// singleJoiningSlash is taken from reverseproxy.go:NewSingleHostReverseProxy
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

// rebaseUrl is taken from reverseproxy.go:NewSingleHostReverseProxy
func rebaseUrl(url *url.URL, onto *url.URL, suffix string) *url.URL {
	newUrl := *url
	newUrl.Scheme = onto.Scheme
	newUrl.Host = onto.Host
	if suffix != "" {
		newUrl.Path = singleJoiningSlash(url.Path, suffix)
	}
	if onto.RawQuery == "" || newUrl.RawQuery == "" {
		newUrl.RawQuery = onto.RawQuery + newUrl.RawQuery
	} else {
		newUrl.RawQuery = onto.RawQuery + "&" + newUrl.RawQuery
	}
	return &newUrl
}

func (api *API) newRequest(r *http.Request, body io.Reader, suffix string) (*http.Request, error) {
	authReq := &http.Request{
		Method: r.Method,
		URL:    rebaseUrl(r.URL, api.URL, suffix),
		Header: helper.HeaderClone(r.Header),
	}
	if body != nil {
		authReq.Body = ioutil.NopCloser(body)
	}

	// Clean some headers when issuing a new request without body
	if body == nil {
		authReq.Header.Del("Content-Type")
		authReq.Header.Del("Content-Encoding")
		authReq.Header.Del("Content-Length")
		authReq.Header.Del("Content-Disposition")
		authReq.Header.Del("Accept-Encoding")

		// Hop-by-hop headers. These are removed when sent to the backend.
		// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html
		authReq.Header.Del("Transfer-Encoding")
		authReq.Header.Del("Connection")
		authReq.Header.Del("Keep-Alive")
		authReq.Header.Del("Proxy-Authenticate")
		authReq.Header.Del("Proxy-Authorization")
		authReq.Header.Del("Te")
		authReq.Header.Del("Trailers")
		authReq.Header.Del("Upgrade")
	}

	// Also forward the Host header, which is excluded from the Header map by the http libary.
	// This allows the Host header received by the backend to be consistent with other
	// requests not going through gitlab-workhorse.
	authReq.Host = r.Host
	// Set a custom header for the request. This can be used in some
	// configurations (Passenger) to solve auth request routing problems.
	authReq.Header.Set("Gitlab-Workhorse", api.Version)

	secretBytes, err := api.Secret.Bytes()
	if err != nil {
		return nil, fmt.Errorf("newRequest: %v", err)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.StandardClaims{Issuer: "gitlab-workhorse"})
	tokenString, err := token.SignedString(secretBytes)
	if err != nil {
		return nil, fmt.Errorf("newRequest: sign JWT: %v", err)
	}
	authReq.Header.Set(RequestHeader, tokenString)

	return authReq, nil
}

func (api *API) PreAuthorizeHandler(h HandleFunc, suffix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authReq, err := api.newRequest(r, nil, suffix)
		if err != nil {
			helper.Fail500(w, r, fmt.Errorf("preAuthorizeHandler newUpstreamRequest: %v", err))
			return
		}

		authResponse, err := api.Client.Do(authReq)
		if err != nil {
			helper.Fail500(w, r, fmt.Errorf("preAuthorizeHandler: do request: %v", err))
			return
		}
		defer authResponse.Body.Close()

		if authResponse.StatusCode != 200 {
			for k, v := range authResponse.Header {
				// Accomodate broken clients that do case-sensitive header lookup
				if k == "Www-Authenticate" {
					w.Header()["WWW-Authenticate"] = v
				} else {
					w.Header()[k] = v
				}
			}
			w.WriteHeader(authResponse.StatusCode)
			io.Copy(w, authResponse.Body)
			return
		}

		if contentType := authResponse.Header.Get("Content-Type"); contentType != ResponseContentType {
			helper.Fail500(w, r, fmt.Errorf("preAuthorizeHandler: API responded with wrong content type: %v", contentType))
			return
		}

		a := &Response{}
		// The auth backend validated the client request and told us additional
		// request metadata. We must extract this information from the auth
		// response body.
		if err := json.NewDecoder(authResponse.Body).Decode(a); err != nil {
			helper.Fail500(w, r, fmt.Errorf("preAuthorizeHandler: decode authorization response: %v", err))
			return
		}
		// Don't hog a TCP connection in CLOSE_WAIT, we can already close it now
		authResponse.Body.Close()

		// Negotiate authentication (Kerberos) may need to return a WWW-Authenticate
		// header to the client even in case of success as per RFC4559.
		for k, v := range authResponse.Header {
			// Case-insensitive comparison as per RFC7230
			if strings.EqualFold(k, "WWW-Authenticate") {
				w.Header()[k] = v
			}
		}

		h(w, r, a)
	})
}
