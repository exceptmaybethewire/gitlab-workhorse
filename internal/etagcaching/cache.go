package etagcaching

import (
	"net/http"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/redis"
)

var sharedStateNamespace = "etag:"

func redisSharedStateKey(r *http.Request) string {
	return sharedStateNamespace + r.URL.Path
}

func etagEqual(ifNoneMatch, etag string) bool {
	return ifNoneMatch == `W/"`+etag+`"`
}

func Cache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ifNoneMatch := r.Header.Get("If-None-Match")
		if ifNoneMatch == "" {
			etagCachingRequestsMissingIfNoneMatch.Inc()
			h.ServeHTTP(w, r)
			return
		}

		redisEtag, err := redis.GetString(redisSharedStateKey(r))
		if err != nil {
			etagCachingRequestsRedisError.Inc()
			h.ServeHTTP(w, r)
			return
		}

		if redisEtag == "" {
			etagCachingRequestsRedisMissingEtag.Inc()
			h.ServeHTTP(w, r)
			return
		}

		if !etagEqual(ifNoneMatch, redisEtag) {
			etagCachingRequestsCacheMiss.Inc()
			h.ServeHTTP(w, r)
			return
		}

		etagCachingRequestsCacheHit.Inc()
		w.Header().Set("ETag", ifNoneMatch)
		w.Header().Set("X-Gitlab-From-Cache", "true")
		w.Header().Set("X-Gitlab-Workhorse-From-Cache", "true")
		w.WriteHeader(http.StatusNotModified)
		return
	})
}
