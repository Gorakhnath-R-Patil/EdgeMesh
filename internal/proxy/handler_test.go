package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/config"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/logging"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/proxy"
)

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return logging.NewWithWriter(buf, config.LoggingConfig{Level: config.LevelDebug, Format: config.FormatJSON}, "test")
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return u
}

func TestHandlerForwardsRequestAndResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("backend received method %q, want POST", r.Method)
		}
		if r.URL.Path != "/orders" {
			t.Errorf("backend received path %q, want /orders", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "hello" {
			t.Errorf("backend received body %q, want %q", body, "hello")
		}
		w.Header().Set("X-Backend", "payment-1")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	}))
	defer backend.Close()

	var logBuf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{Upstream: mustParseURL(t, backend.URL)}, testLogger(&logBuf))

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Body.String(); got != "created" {
		t.Errorf("body = %q, want %q", got, "created")
	}
	if got := rec.Header().Get("X-Backend"); got != "payment-1" {
		t.Errorf("X-Backend header = %q, want %q", got, "payment-1")
	}

	assertLogField(t, logBuf.String(), "status", float64(http.StatusCreated))
}

func TestHandlerSetsXForwardedFor(t *testing.T) {
	var gotXFF string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	var logBuf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{Upstream: mustParseURL(t, backend.URL)}, testLogger(&logBuf))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotXFF != "203.0.113.5" {
		t.Errorf("X-Forwarded-For = %q, want %q", gotXFF, "203.0.113.5")
	}
}

func TestHandlerReturns502WhenBackendUnreachable(t *testing.T) {
	// A backend that has never been listening; connection attempts
	// fail immediately with "connection refused" rather than timing
	// out, keeping the test fast.
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unreachable := backend.URL
	backend.Close()

	var logBuf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{
		Upstream:    mustParseURL(t, unreachable),
		DialTimeout: 2 * time.Second,
	}, testLogger(&logBuf))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	assertLogField(t, logBuf.String(), "status", float64(http.StatusBadGateway))
}

func TestHandlerReturns504WhenBackendExceedsRequestTimeout(t *testing.T) {
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hang until the test releases it, well past the proxy's timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		backend.Close()
	}()

	var logBuf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{
		Upstream:       mustParseURL(t, backend.URL),
		RequestTimeout: 100 * time.Millisecond,
	}, testLogger(&logBuf))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
	if elapsed > 2*time.Second {
		t.Errorf("handler took %v to time out, want well under 2s", elapsed)
	}
	assertLogField(t, logBuf.String(), "status", float64(http.StatusGatewayTimeout))
}

func TestHandlerAppliesDefaultsWhenConfigZeroValued(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	var logBuf bytes.Buffer
	// No timeouts/pool sizes set — must not panic or hang forever.
	handler := proxy.NewHandler(proxy.Config{Upstream: mustParseURL(t, backend.URL)}, testLogger(&logBuf))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandlerLogsMethodAndPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	var logBuf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{Upstream: mustParseURL(t, backend.URL)}, testLogger(&logBuf))

	req := httptest.NewRequest(http.MethodGet, "/payment/123", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assertLogField(t, logBuf.String(), "method", "GET")
	assertLogField(t, logBuf.String(), "path", "/payment/123")
}

// assertLogField parses logLine (one JSON object per line, as produced
// by the json logging format) and fails the test unless at least one
// line has field == want.
func assertLogField(t *testing.T, logOutput string, field string, want any) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(logOutput), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not valid JSON: %v\nline: %s", err, line)
		}
		if record[field] == want {
			return
		}
	}
	t.Fatalf("no log line had %s = %v\noutput: %s", field, want, logOutput)
}
