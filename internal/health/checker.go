// Package health implements EdgeMesh's active health checking: probing
// registered endpoints on a schedule, applying hysteresis so a single
// blip doesn't flap an endpoint's state, and writing the result back to
// the service registry (internal/registry).
//
// Day 7 scope is deliberately the two-state model called out in the
// architecture: HEALTHY and UNHEALTHY, driven only by active probes.
// The fuller HEALTHY -> DEGRADED -> UNHEALTHY -> RECOVERING -> HEALTHY
// state machine, driven by passive request-outcome signals, is a later
// development phase; this package never produces DEGRADED or
// RECOVERING, and treats an endpoint's HEALTH_STATE_UNSPECIFIED zero
// value (not yet checked) as "needs SuccessThreshold consecutive
// successes before being promoted to HEALTHY," the same as any other
// non-HEALTHY state.
package health

import (
	"context"
	"fmt"
	"io"
	"net/http"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// Checker probes a single endpoint and reports whether it's healthy.
// Check returning nil means healthy; any non-nil error means
// unhealthy, and its message is logged as the reason.
//
// Implementations must respect ctx's deadline/cancellation — Monitor
// derives a per-check context from HealthCheckPolicy.Timeout and relies
// on Checker to honor it so one slow endpoint can't stall an entire
// check pass.
type Checker interface {
	Check(ctx context.Context, ep *meshv1alpha1.Endpoint) error
}

// CheckerFunc adapts a plain function to a Checker, the same pattern as
// http.HandlerFunc.
type CheckerFunc func(ctx context.Context, ep *meshv1alpha1.Endpoint) error

// Check implements Checker.
func (f CheckerFunc) Check(ctx context.Context, ep *meshv1alpha1.Endpoint) error {
	return f(ctx, ep)
}

// defaultHealthPath is used when HTTPChecker.Path is empty.
const defaultHealthPath = "/healthz"

// HTTPChecker checks an endpoint by issuing an HTTP GET to Path against
// the endpoint's Address:Port, treating any 2xx response as healthy and
// anything else — a non-2xx status, a connection failure, or the
// check's context deadline expiring — as unhealthy.
type HTTPChecker struct {
	// Path is the request path probed on every endpoint, e.g.
	// "/healthz". Defaults to "/healthz" if empty.
	Path string
	// Client performs the request. Defaults to http.DefaultClient if
	// nil. The per-check timeout comes from the context Monitor passes
	// to Check, not from Client, so the default is safe to use even
	// though it has no Timeout of its own.
	Client *http.Client
}

// Check implements Checker.
func (c HTTPChecker) Check(ctx context.Context, ep *meshv1alpha1.Endpoint) error {
	path := c.Path
	if path == "" {
		path = defaultHealthPath
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}

	url := fmt.Sprintf("http://%s:%d%s", ep.GetAddress(), ep.GetPort(), path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("health: building request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health: request failed: %w", err)
	}
	defer resp.Body.Close()
	// Drain the body so the underlying connection can be reused by the
	// client's transport instead of being closed on every check.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health: unhealthy status %d", resp.StatusCode)
	}
	return nil
}
