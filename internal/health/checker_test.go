package health_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/health"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// endpointFor builds an Endpoint pointing at the host:port of an
// httptest.Server, since HTTPChecker addresses endpoints by
// Address:Port, not by an arbitrary base URL.
func endpointFor(t *testing.T, server *httptest.Server) *meshv1alpha1.Endpoint {
	t.Helper()
	host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error = %v", server.Listener.Addr().String(), err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi(%q) error = %v", portStr, err)
	}
	return &meshv1alpha1.Endpoint{Id: "ep-1", ServiceName: "svc", Address: host, Port: uint32(port)}
}

func TestHTTPCheckerReturnsNilOn2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := health.HTTPChecker{}
	if err := c.Check(context.Background(), endpointFor(t, server)); err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
}

func TestHTTPCheckerReturnsErrorOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := health.HTTPChecker{}
	if err := c.Check(context.Background(), endpointFor(t, server)); err == nil {
		t.Fatal("Check() error = nil, want error for a 503 response")
	}
}

func TestHTTPCheckerReturnsErrorOnConnectionRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ep := endpointFor(t, server)
	server.Close() // nothing listening anymore

	c := health.HTTPChecker{}
	if err := c.Check(context.Background(), ep); err == nil {
		t.Fatal("Check() error = nil, want error for a closed backend")
	}
}

func TestHTTPCheckerRespectsContextTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	c := health.HTTPChecker{}
	start := time.Now()
	err := c.Check(ctx, endpointFor(t, server))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Check() error = nil, want a timeout error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Check() took %v to respect a 50ms context timeout, want well under 2s", elapsed)
	}
}

func TestHTTPCheckerDefaultPath(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := health.HTTPChecker{}
	if err := c.Check(context.Background(), endpointFor(t, server)); err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	if gotPath != "/healthz" {
		t.Errorf("path = %q, want default %q", gotPath, "/healthz")
	}
}

func TestHTTPCheckerCustomPath(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := health.HTTPChecker{Path: "/status"}
	if err := c.Check(context.Background(), endpointFor(t, server)); err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	if gotPath != "/status" {
		t.Errorf("path = %q, want %q", gotPath, "/status")
	}
}

func TestCheckerFuncAdaptsPlainFunction(t *testing.T) {
	var calledWith *meshv1alpha1.Endpoint
	c := health.CheckerFunc(func(_ context.Context, ep *meshv1alpha1.Endpoint) error {
		calledWith = ep
		return nil
	})

	ep := &meshv1alpha1.Endpoint{Id: "ep-1"}
	if err := c.Check(context.Background(), ep); err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	if calledWith != ep {
		t.Error("CheckerFunc did not forward the endpoint to the wrapped function")
	}
}
