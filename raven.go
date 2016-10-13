package main

import (
	"net/http"
	"os"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"

	"github.com/getsentry/raven-go"
)

func wrapRaven(h http.Handler) http.Handler {
	// Use a custom environment variable (not SENTRY_DSN) to prevent
	// clashes with gitlab-rails.
	sentryDSN := os.Getenv("GITLAB_WORKHORSE_SENTRY_DSN")
	raven.SetDSN(sentryDSN) // sentryDSN may be empty

	if sentryDSN == "" {
		return h
	}

	raven.DefaultClient.SetRelease(Version)

	return http.HandlerFunc(raven.RecoveryHandler(
		func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					helper.CleanHeadersForRaven(r)
					panic(p)
				}
			}()

			h.ServeHTTP(w, r)
		}))
}
