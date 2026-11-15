package diagnostichttp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/araihu/oie/internal/diagnostics"
)

const correlationHeader = "X-Oie-Diagnostic-Trace"

type loggerKey struct{}

func WithLogger(r *http.Request, logger *diagnostics.Logger) *http.Request {
	if logger == nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), loggerKey{}, logger))
}

func HTTPProblem(r *http.Request, status int) {
	l, _ := r.Context().Value(loggerKey{}).(*diagnostics.Logger)
	if status >= 500 && status <= 599 && r.URL.Path != "/api/v1/health" {
		l.RecordHTTP(diagnostics.EngineHTTPError, 0, status, r.Header.Get(correlationHeader))
	}
}

type transport struct {
	next http.RoundTripper
	log  *diagnostics.Logger
}

func Transport(l *diagnostics.Logger, next http.RoundTripper) http.RoundTripper {
	if l == nil {
		return next
	}
	return &transport{next: next, log: l}
}

func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/api/v1/health" {
		return t.next.RoundTrip(r)
	}
	request := r.Clone(r.Context())
	var random [16]byte
	rand.Read(random[:])
	trace := fmt.Sprintf("%x", random)
	request.Header.Set(correlationHeader, trace)
	started := time.Now()
	response, err := t.next.RoundTrip(request)
	elapsed := time.Since(started)
	switch {
	case err == nil && response.StatusCode >= 500 && response.StatusCode <= 599:
		t.log.RecordHTTP(diagnostics.UpstreamHTTPError, elapsed, response.StatusCode, trace)
	case err != nil && !errors.Is(err, context.Canceled):
		event := diagnostics.EngineUnavailable
		var network net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()) {
			event = diagnostics.EngineTimeout
		}
		t.log.RecordHTTP(event, elapsed, 0, trace)
	}
	return response, err
}
