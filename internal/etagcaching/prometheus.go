package etagcaching

import "github.com/prometheus/client_golang/prometheus"

var (
	etagCachingRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_etag_caching_requests",
			Help: "How many etag caching requests have been processed",
		},
		[]string{"status"},
	)

	etagCachingRequestsMissingIfNoneMatch = etagCachingRequests.WithLabelValues("missing-if-none-match")
	etagCachingRequestsRedisError         = etagCachingRequests.WithLabelValues("redis-error")
	etagCachingRequestsRedisMissingEtag   = etagCachingRequests.WithLabelValues("cache-missing-etag")
	etagCachingRequestsCacheMiss          = etagCachingRequests.WithLabelValues("cache-miss")
	etagCachingRequestsCacheHit           = etagCachingRequests.WithLabelValues("cache-hit")
)

func init() {
	prometheus.MustRegister(
		etagCachingRequests)
}
