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
	defaultDialTimeout         = 5 * time.Second
	defaultRequestTimeout      = 15 * time.Second
	defaultIdleConnTimeout     = 90 * time.Second
	defaultMaxIdleConns        = 100
	defaultMaxIdleConnsPerHost = 10
)

// Config configures a Handler's forwarding to a single upstream
// backend.
type Config struct {
	// Upstream is the backend every request is forwarded to.
	Upstream *url.URL

	// DialTimeout bounds establishing a new TCP connection to the
	// backend.
	DialTimeout time.Duration
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
		IdleConnTimeout:     cfg.IdleConnTimeout,
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
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
			if errors.Is(err, context.DeadlineExceeded) {
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
