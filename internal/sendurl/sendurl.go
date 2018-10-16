package sendurl

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/log"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
)

type entry struct{ senddata.Prefix }

type entryParams struct {
	URL            string
	AllowRedirects bool
}

var SendURL = &entry{"send-url:"}

var rangeHeaderKeys = []string{
	"If-Match",
	"If-Unmodified-Since",
	"If-None-Match",
	"If-Modified-Since",
	"If-Range",
	"Range",
}

// httpTransport defines a http.Transport with values
// that are more restrictive than for http.DefaultTransport,
// they define shorter TLS Handshake, and more agressive connection closing
// to prevent the connection hanging and reduce FD usage
var httpTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 10 * time.Second,
	}).DialContext,
	MaxIdleConns:          2,
	IdleConnTimeout:       30 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 10 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}

var httpClient = &http.Client{
	Transport: httpTransport,
}

var (
	sendURLRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_send_url_requests",
			Help: "How many send URL requests have been processed",
		},
		[]string{"status"},
	)
	sendURLOpenRequests = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gitlab_workhorse_send_url_open_requests",
			Help: "Describes how many send URL requests are open now",
		},
	)
	sendURLBytes = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_send_url_bytes",
			Help: "How many bytes were passed with send URL",
		},
	)

	sendURLRequestsInvalidData   = sendURLRequests.WithLabelValues("invalid-data")
	sendURLRequestsRequestFailed = sendURLRequests.WithLabelValues("request-failed")
	sendURLRequestsSucceeded     = sendURLRequests.WithLabelValues("succeeded")
)

func init() {
	prometheus.MustRegister(
		sendURLRequests,
		sendURLOpenRequests,
		sendURLBytes)
}

func (e *entry) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params entryParams

	sendURLOpenRequests.Inc()
	defer sendURLOpenRequests.Dec()

	if err := e.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendURL: unpack sendData: %v", err))
		return
	}

	log.WithFields(r.Context(), log.Fields{
		"url":  helper.ScrubURLParams(params.URL),
		"path": r.URL.Path,
	}).Print("SendURL: sending")

	if params.URL == "" {
		sendURLRequestsInvalidData.Inc()
		helper.Fail500(w, r, fmt.Errorf("SendURL: URL is empty"))
		return
	}

	// create new request and copy range headers
	newReq, err := http.NewRequest("GET", params.URL, nil)
	if err != nil {
		sendURLRequestsInvalidData.Inc()
		helper.Fail500(w, r, fmt.Errorf("SendURL: NewRequest: %v", err))
		return
	}

	for _, header := range rangeHeaderKeys {
		newReq.Header[header] = r.Header[header]
	}

	// execute new request
	var resp *http.Response
	if params.AllowRedirects {
		resp, err = httpClient.Do(newReq)
	} else {
		resp, err = httpTransport.RoundTrip(newReq)
	}
	if err != nil {
		sendURLRequestsRequestFailed.Inc()
		helper.Fail500(w, r, fmt.Errorf("SendURL: Do request: %v", err))
		return
	}

	// copy response headers and body
	for key, value := range resp.Header {
		w.Header()[key] = value
	}
	w.WriteHeader(resp.StatusCode)

	defer resp.Body.Close()
	n, err := io.Copy(w, resp.Body)
	sendURLBytes.Add(float64(n))

	if err != nil {
		sendURLRequestsRequestFailed.Inc()
		helper.Fail500(w, r, fmt.Errorf("SendURL: Copy response: %v", err))
		return
	}

	sendURLRequestsSucceeded.Inc()
}
