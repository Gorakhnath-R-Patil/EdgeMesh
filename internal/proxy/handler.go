// Package proxy implements EdgeMesh's data-plane request forwarding:
// receive a request, forward it to a backend over a pooled connection,
// bound the whole exchange by a timeout, and observe the outcome.
//
// This is deliberately the full extent of the proxy's job today. It
// forwards to a single, statically configured backend — no endpoint
// selection, no health awareness, no retries. Those arrive once the
// service registry and routing/health/retry engines exist in later
// development phases; putting that logic here now would violate the
// data plane's core constraint: no business logic in the proxy.
//
// Three independent timeout phases bound a proxied request, matching
// how real reverse proxies separate these concerns (e.g. Envoy, Nginx):
//
//   - DialTimeout ("connection timeout") bounds establishing a new TCP
//     connection to the backend.
//   - ResponseHeaderTimeout ("upstream timeout") bounds waiting for the
//     backend to start responding once the request has been sent — a
//     backend that accepts a connection but never answers is caught
//     here, distinctly from a backend that's simply slow to dial.
//   - RequestTimeout ("request timeout") bounds the entire exchange —
//     connect, write, and read the full response — a superset of the
//     other two phases, catching a backend that starts responding but
//     stalls partway through.
//
// Every phase composes correctly with an incoming request's own
// context: Go's context.WithTimeout always takes the earlier of a
// parent's existing deadline and the new one, so a caller that already
// set a shorter deadline is never silently extended, and canceling the
// incoming request's context (e.g. the client disconnecting) always
// propagates through to cancel the in-flight backend call — see
// handler_test.go for tests proving both properties.
package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Default connection-management settings, used whenever the
// corresponding Config field is left at its zero value. Callers loading
// Config from internal/config get sensible values from that package's
// own defaulting instead; these exist so Config is also safe to
// construct directly (e.g. in tests) without every field set.
const (
	defaultDialTimeout           = 5 * time.Second
	defaultResponseHeaderTimeout = 10 * time.Second
	defaultRequestTimeout        = 15 * time.Second
	defaultIdleConnTimeout       = 90 * time.Second
	defaultMaxIdleConns          = 100
	defaultMaxIdleConnsPerHost   = 10
)

// timeoutError matches any error that identifies itself as a timeout
// via the standard net.Error-style Timeout() bool method. That single
// check covers all three timeout phases uniformly, even though they're
// three unrelated error types under the hood: the incoming context's
// deadline expiring (context.DeadlineExceeded), net.Dialer's own dial
// timeout (*net.OpError), and http.Transport's ResponseHeaderTimeout
// (an internal net/http error type) all implement it.
type timeoutError interface {
	Timeout() bool
}

// isTimeout reports whether err is, or wraps, a timeoutError whose
// Timeout() reports true — the basis for choosing 504 Gateway Timeout
// over 502 Bad Gateway.
func isTimeout(err error) bool {
	var te timeoutError
	return errors.As(err, &te) && te.Timeout()
}

// Config configures a Handler's forwarding to a single upstream
// backend.
type Config struct {
	// Upstream is the backend every request is forwarded to.
	Upstream *url.URL

	// DialTimeout ("connection timeout") bounds establishing a new TCP
	// connection to the backend.
	DialTimeout time.Duration
	// ResponseHeaderTimeout ("upstream timeout") bounds waiting for the
	// backend to start responding after the request has been sent —
	// independent of DialTimeout (connecting) and RequestTimeout (the
	// whole exchange).
	ResponseHeaderTimeout time.Duration
	// RequestTimeout bounds the entire proxied request — connect,
	// write, and read the response — not just the initial connect.
	RequestTimeout time.Duration
	// IdleConnTimeout bounds how long a pooled, idle backend
	// connection is kept open for reuse.
	IdleConnTimeout time.Duration
	// MaxIdleConns and MaxIdleConnsPerHost bound the pooled connection
	// cache size shared across all backend connections.
	MaxIdleConns        int
	MaxIdleConnsPerHost int
}

func (c Config) withDefaults() Config {
	if c.DialTimeout <= 0 {
		c.DialTimeout = defaultDialTimeout
	}
	if c.ResponseHeaderTimeout <= 0 {
		c.ResponseHeaderTimeout = defaultResponseHeaderTimeout
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultRequestTimeout
	}
	if c.IdleConnTimeout <= 0 {
		c.IdleConnTimeout = defaultIdleConnTimeout
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = defaultMaxIdleConns
	}
	if c.MaxIdleConnsPerHost <= 0 {
		c.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}
	return c
}

// NewHandler builds an http.Handler that forwards every request to
// cfg.Upstream over a pooled, timeout-bounded connection, translates
// backend failures into 502/504 responses, and logs each request's
// outcome through logger. cfg.Upstream must not be nil.
func NewHandler(cfg Config, logger *slog.Logger) http.Handler {
	cfg = cfg.withDefaults()

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: cfg.DialTimeout,
		}).DialContext,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(cfg.Upstream)
			pr.SetXForwarded()
		},
		Transport: transport,
		ErrorLog:  slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			status := http.StatusBadGateway
			if isTimeout(err) {
				status = http.StatusGatewayTimeout
			}
			if rec, ok := w.(*statusRecorder); ok {
				rec.err = err
			}
			w.WriteHeader(status)
		},
	}

	return &loggingHandler{
		next:           rp,
		logger:         logger,
		requestTimeout: cfg.RequestTimeout,
		upstream:       cfg.Upstream.String(),
	}
}

// loggingHandler bounds each request to requestTimeout, forwards it to
// next, and emits one structured log record per request describing the
// outcome.
type loggingHandler struct {
	next           http.Handler
	logger         *slog.Logger
	requestTimeout time.Duration
	upstream       string
}

func (h *loggingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimeout)
	defer cancel()

	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	h.next.ServeHTTP(rec, r.WithContext(ctx))

	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"upstream", h.upstream,
		"status", rec.status,
		"bytes", rec.bytes,
		"duration_ms", time.Since(start).Milliseconds(),
	}
	if rec.err != nil {
		h.logger.Warn("proxied request failed", append(attrs, "error", rec.err.Error())...)
		return
	}
	h.logger.Info("proxied request", attrs...)
}

// statusRecorder captures the status code and byte count written to an
// http.ResponseWriter, and — set by Handler's ErrorHandler on a gateway
// failure — the error that caused it, so loggingHandler can report both
// after ServeHTTP returns.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	err    error
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}
