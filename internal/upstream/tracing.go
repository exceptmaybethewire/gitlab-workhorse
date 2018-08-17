package upstream

import (
	"net/http"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/log"
)

func traceRoute(next http.Handler, method string, regexpStr string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var serverSpan opentracing.Span
		appSpecificOperationName := "MOOOO"
		wireContext, err := opentracing.GlobalTracer().Extract(
			opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(r.Header))
		if err != nil {
			log.WithContext(r.Context()).WithError(err).Debug("Trace setup failed")
		}

		// Create the span referring to the RPC client if available.
		// If wireContext == nil, a root span will be created.
		serverSpan = opentracing.StartSpan(
			appSpecificOperationName,
			ext.RPCServerOption(wireContext))

		defer serverSpan.Finish()

		// TODO(andrew): example uses Background here. Why?
		ctx := opentracing.ContextWithSpan(r.Context(), serverSpan)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
