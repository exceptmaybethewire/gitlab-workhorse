package helper

import (
	"bytes"
	"errors"
	"io/ioutil"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"

	log "github.com/sirupsen/logrus"
)

const NginxResponseBufferHeader = "X-Accel-Buffering"

var scrubRegexp = regexp.MustCompile(`(?i)([\?&]((?:private|authenticity|rss)[\-_]token)|(?:X-AMZ-)?Signature)=[^&]*`)

func Fail500(w http.ResponseWriter, r *http.Request, err error) {
	http.Error(w, "Internal server error", 500)
	if err != nil {
		captureRavenError(r, err)
	}

	printError(r, err)
}

func LogError(r *http.Request, err error) {
	if err != nil {
		captureRavenError(r, err)
	}

	printError(r, err)
}

func RequestEntityTooLarge(w http.ResponseWriter, r *http.Request, err error) {
	http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
	captureRavenError(r, err)
	printError(r, err)
}

func printError(r *http.Request, err error) {
	if r != nil {
		log.WithFields(log.Fields{
			"method": r.Method,
			"uri":    r.RequestURI,
		}).Printf("error: %s %q: %v", r.Method, r.RequestURI, err)
	} else {
		log.WithError(err).Print("error")
	}
}

func SetNoCacheHeaders(header http.Header) {
	header.Set("Cache-Control", "no-cache, no-store, max-age=0, must-revalidate")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "Fri, 01 Jan 1990 00:00:00 GMT")
}

func OpenFile(path string) (file *os.File, fi os.FileInfo, err error) {
	file, err = os.Open(path)
	if err != nil {
		return
	}

	defer func() {
		if err != nil {
			file.Close()
		}
	}()

	fi, err = file.Stat()
	if err != nil {
		return
	}

	// The os.Open can also open directories
	if fi.IsDir() {
		err = &os.PathError{
			Op:   "open",
			Path: path,
			Err:  errors.New("path is directory"),
		}
		return
	}

	return
}

func URLMustParse(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		log.WithField("url", s).WithError(err).Fatal("urlMustParse")
	}
	return u
}

func HTTPError(w http.ResponseWriter, r *http.Request, error string, code int) {
	if r.ProtoAtLeast(1, 1) {
		// Force client to disconnect if we render request error
		w.Header().Set("Connection", "close")
	}

	http.Error(w, error, code)
}

func HeaderClone(h http.Header) http.Header {
	h2 := make(http.Header, len(h))
	for k, vv := range h {
		vv2 := make([]string, len(vv))
		copy(vv2, vv)
		h2[k] = vv2
	}
	return h2
}

func CleanUpProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	process := cmd.Process
	if process != nil && process.Pid > 0 {
		// Send SIGTERM to the process group of cmd
		syscall.Kill(-process.Pid, syscall.SIGTERM)
	}

	// reap our child process
	cmd.Wait()
}

func ExitStatus(err error) (int, bool) {
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		return 0, false
	}

	waitStatus, ok := exitError.Sys().(syscall.WaitStatus)
	if !ok {
		return 0, false
	}

	return waitStatus.ExitStatus(), true
}

func DisableResponseBuffering(w http.ResponseWriter) {
	w.Header().Set(NginxResponseBufferHeader, "no")
}

func AllowResponseBuffering(w http.ResponseWriter) {
	w.Header().Del(NginxResponseBufferHeader)
}

func SetForwardedFor(newHeaders *http.Header, originalRequest *http.Request) {
	if clientIP, _, err := net.SplitHostPort(originalRequest.RemoteAddr); err == nil {
		var header string

		// If we aren't the first proxy retain prior
		// X-Forwarded-For information as a comma+space
		// separated list and fold multiple headers into one.
		if prior, ok := originalRequest.Header["X-Forwarded-For"]; ok {
			header = strings.Join(prior, ", ") + ", " + clientIP
		} else {
			header = clientIP
		}
		newHeaders.Set("X-Forwarded-For", header)
	}
}

func IsContentType(expected, actual string) bool {
	parsed, _, err := mime.ParseMediaType(actual)
	return err == nil && parsed == expected
}

func IsApplicationJson(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	return IsContentType("application/json", contentType)
}

func ReadRequestBody(w http.ResponseWriter, r *http.Request, maxBodySize int64) ([]byte, error) {
	limitedBody := http.MaxBytesReader(w, r.Body, maxBodySize)
	defer limitedBody.Close()

	return ioutil.ReadAll(limitedBody)
}

func CloneRequestWithNewBody(r *http.Request, body []byte) *http.Request {
	newReq := *r
	newReq.Body = ioutil.NopCloser(bytes.NewReader(body))
	newReq.Header = HeaderClone(r.Header)
	newReq.ContentLength = int64(len(body))
	return &newReq
}

// ScrubURLParams replaces the content of any sensitive query string parameters
// in an URL with `[FILTERED]`
func ScrubURLParams(url string) string {
	return scrubRegexp.ReplaceAllString(url, "$1=[FILTERED]")
}
