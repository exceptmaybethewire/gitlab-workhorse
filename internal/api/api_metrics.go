package api

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	requestsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_internal_api_requests",
			Help: "How many internal API requests have been completed by gitlab-workhorse, partitioned by status code and HTTP method.",
		},
		[]string{"code", "method"},
	)
	bytesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_internal_api_failure_response_bytes",
			Help: "How many bytes have been returned by upstream GitLab in API failure/rejection response bodies.",
		},
	)
)

func init() {
	prometheus.MustRegister(requestsCounter)
	prometheus.MustRegister(bytesTotal)
}
