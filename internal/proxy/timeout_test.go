package proxy_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/proxy"
)

// ---- Upstream timeout (ResponseHeaderTimeout): a backend that accepts
// the connection but never sends a response, distinct from a backend
// that's slow to accept the connection in the first place (dial
// timeout) or one that starts responding but stalls mid-body (overall
// request timeout). ----

func TestHandlerReturns504WhenUpstreamTimeoutExpiresBeforeHeaders(t *testing.T) {
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // never send headers until released, well past the upstream timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		backend.Close()
	}()

	var buf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{
		Upstream:              mustParseURL(t, backend.URL),
		ResponseHeaderTimeout: 100 * time.Millisecond,
		RequestTimeout:        5 * time.Second, // deliberately much larger
	}, testLogger(&buf))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d (upstream timeout)", rec.Code, http.StatusGatewayTimeout)
	}
	// If this took anywhere near RequestTimeout (5s), ResponseHeaderTimeout
	// didn't actually fire on its own -- the overall request timeout did.
	if elapsed > 2*time.Second {
		t.Errorf("handler took %v, want well under RequestTimeout (5s) -- ResponseHeaderTimeout (100ms) should have fired first", elapsed)
	}
}

// ---- Connection timeout: a backend that never answers the TCP
// handshake at all, so the dial itself never completes. 192.0.2.1 is
// TEST-NET-1 (RFC 5737): reserved for documentation and testing, never
// assigned to a real host, so connecting to it reliably goes
// unanswered rather than being immediately refused the way a closed
// local port is (see TestHandlerReturns502WhenBackendUnreachable) —
// exactly the "slow to connect" case DialTimeout exists to bound. ----

const blackholeAddress = "http://192.0.2.1:80"

func TestHandlerReturns504WhenDialTimeoutExpires(t *testing.T) {
	var buf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{
		Upstream:    mustParseURL(t, blackholeAddress),
		DialTimeout: 300 * time.Millisecond,
	}, testLogger(&buf))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d (dial timeout)", rec.Code, http.StatusGatewayTimeout)
	}
	// Bounded well below what an unconfigured OS-level connect timeout
	// would take (often tens of seconds) -- proving DialTimeout, not
	// some much larger default, is what ended the attempt.
	if elapsed > 5*time.Second {
		t.Errorf("handler took %v to respect a 300ms DialTimeout, want well under 5s", elapsed)
	}
}

// ---- Context propagation / cancellation ----

// TestHandlerClientCancellationPropagatesToBackend proves that
// canceling the incoming request's context — simulating the client
// disconnecting — propagates through to cancel the in-flight backend
// request, rather than the backend call running to completion
// regardless.
func TestHandlerClientCancellationPropagatesToBackend(t *testing.T) {
	backendSawCancellation := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			close(backendSawCancellation)
		case <-time.After(5 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer backend.Close()

	var buf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{
		Upstream:       mustParseURL(t, backend.URL),
		RequestTimeout: 5 * time.Second, // long enough that only cancellation, not this, ends the request
	}, testLogger(&buf))

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	served := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(served)
	}()

	time.Sleep(50 * time.Millisecond) // let the request reach the backend
	cancel()                          // simulate the client disconnecting

	select {
	case <-backendSawCancellation:
	case <-time.After(2 * time.Second):
		t.Fatal("backend never observed context cancellation within 2s of the client canceling")
	}

	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeHTTP did not return within 2s of the client's context being canceled")
	}
}

// TestHandlerCallerDeadlineIsNotExtended proves that an incoming
// request's own, shorter deadline is respected rather than silently
// extended by the proxy's own (longer) configured RequestTimeout --
// Go's context.WithTimeout always takes the earlier of a parent's
// existing deadline and the new one, and this test locks that
// composition in as a guaranteed, tested property rather than an
// incidental one.
func TestHandlerCallerDeadlineIsNotExtended(t *testing.T) {
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		backend.Close()
	}()

	var buf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{
		Upstream:       mustParseURL(t, backend.URL),
		RequestTimeout: 5 * time.Second, // far longer than the caller's own deadline below
	}, testLogger(&buf))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
	if elapsed > 2*time.Second {
		t.Errorf("handler took %v, want it to respect the caller's 100ms deadline rather than waiting out its own 5s RequestTimeout", elapsed)
	}
}

// TestHandlerDoesNotCancelSiblingRequests is a concurrency check for
// context propagation: canceling one request's context must not affect
// any other concurrently in-flight request through the shared Handler.
func TestHandlerDoesNotCancelSiblingRequests(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	var buf bytes.Buffer
	handler := proxy.NewHandler(proxy.Config{
		Upstream:       mustParseURL(t, backend.URL),
		RequestTimeout: 5 * time.Second,
	}, testLogger(&buf))

	const goroutines = 10
	var succeeded int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			if i == 0 {
				// Only the first request gets canceled, immediately.
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				atomic.AddInt64(&succeeded, 1)
			}
		}(i)
	}
	wg.Wait()

	// 9 of the 10 (every request except the pre-canceled one) must
	// succeed -- if canceling request 0 leaked into shared state and
	// affected the others, this count would be lower.
	if succeeded != goroutines-1 {
		t.Errorf("succeeded = %d, want %d (all requests except the pre-canceled one)", succeeded, goroutines-1)
	}
}
