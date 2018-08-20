package proxy

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptrace"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/opentracing/opentracing-go/log"
)

func sendRequestWithTracing(ctx context.Context, req *http.Request, inner func(req *http.Request)) {
	var parentCtx opentracing.SpanContext
	parentSpan := opentracing.SpanFromContext(ctx)
	if parentSpan != nil {
		parentCtx = parentSpan.Context()
	}

	// start a new Span to wrap HTTP request
	span := opentracing.StartSpan(
		"reverse proxy",
		opentracing.ChildOf(parentCtx),
	)

	// make sure the Span is finished once we're done
	defer span.Finish()

	ctx = opentracing.ContextWithSpan(ctx, span)

	// attach ClientTrace to the Context, and Context to request
	trace := newClientTrace(span)
	ctx = httptrace.WithClientTrace(ctx, trace)
	req = req.WithContext(ctx)

	ext.SpanKindRPCClient.Set(span)
	ext.HTTPUrl.Set(span, req.URL.String())
	ext.HTTPMethod.Set(span, req.Method)
	span.Tracer().Inject(
		span.Context(),
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(req.Header),
	)

	inner(req)
}

func newClientTrace(span opentracing.Span) *httptrace.ClientTrace {
	trace := &clientTrace{span: span}
	return &httptrace.ClientTrace{
		GotFirstResponseByte: trace.gotFirstResponseByte,
		ConnectStart:         trace.connectStart,
		ConnectDone:          trace.connectDone,
		TLSHandshakeStart:    trace.tlsHandshakeStart,
		TLSHandshakeDone:     trace.tlsHandshakeDone,
		WroteHeaders:         trace.wroteHeaders,
		WroteRequest:         trace.wroteRequest,
	}
}

// clientTrace holds a reference to the Span and
// provides methods used as ClientTrace callbacks
type clientTrace struct {
	span opentracing.Span
}

func (h *clientTrace) dnsStart(info httptrace.DNSStartInfo) {
	h.span.LogFields(
		log.String("event", "DNS start"),
		log.Object("host", info.Host),
	)
}

func (h *clientTrace) dnsDone(httptrace.DNSDoneInfo) {
	h.span.LogFields(log.String("event", "DNS done"))
}

func (h *clientTrace) gotFirstResponseByte() {
	h.span.LogFields(log.String("event", "First Response Byte"))
}
func (h *clientTrace) connectStart(network, addr string) {
	h.span.LogFields(
		log.String("event", "Connect Start"),
		log.String("network", network),
		log.String("addr", addr),
	)
}
func (h *clientTrace) connectDone(network, addr string, err error) {
	h.span.LogFields(
		log.String("event", "Connect Done"),
		log.String("network", network),
		log.String("addr", addr),
		log.Object("error", err),
	)
}

func (h *clientTrace) tlsHandshakeStart() {
	h.span.LogFields(log.String("event", "TLS Handshake Start"))
}
func (h *clientTrace) tlsHandshakeDone(state tls.ConnectionState, err error) {
	h.span.LogFields(
		log.String("event", "TLS Handshake Done"),
		log.Object("error", err),
	)
}
func (h *clientTrace) wroteHeaders() {
	h.span.LogFields(log.String("event", "Wrote Headers"))
}

func (h *clientTrace) wroteRequest(info httptrace.WroteRequestInfo) {
	h.span.LogFields(
		log.String("event", "Wrote Request Info"),
		log.Object("error", info.Err),
	)
}
