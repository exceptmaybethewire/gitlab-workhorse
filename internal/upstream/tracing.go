package upstream

import (
	"fmt"
	"net/http"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/log"
)

func traceRoute(next http.Handler, method string, regexpStr string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var serverSpan opentracing.Span
		wireContext, err := opentracing.GlobalTracer().Extract(
			opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(r.Header))
		if err != nil {
			log.WithContext(r.Context()).WithError(err).Debug("Trace setup failed")
		}

		correlationID := r.Context().Value(log.KeyCorrelationID)

		var operationName string

		// TODO: if would be nice to move away from identifying routes by a regexp and switch to readable identifiers
		if regexpStr == "" {
			regexpStr = "default"
		}

		if method == "" {
			operationName = "route " + regexpStr
		} else {
			operationName = fmt.Sprintf("route %v %v", method, regexpStr)
		}

		// Create the span referring to the RPC client if available.
		// If wireContext == nil, a root span will be created.
		serverSpan = opentracing.StartSpan(
			operationName,
			ext.RPCServerOption(wireContext),
			opentracing.Tag{Key: "Correlation-ID", Value: correlationID},
		)

		defer serverSpan.Finish()

		// TODO(andrew): example uses Background here. Find out why?
		ctx := opentracing.ContextWithSpan(r.Context(), serverSpan)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
