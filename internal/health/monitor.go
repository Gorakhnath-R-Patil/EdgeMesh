package health

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/registry"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// Built-in defaults, applied to any field left zero-valued in the
// HealthCheckPolicy passed to NewMonitor or NewPassiveTracker. A nil
// policy gets every default.
const (
	defaultInterval         = 10 * time.Second
	defaultTimeout          = 2 * time.Second
	defaultFailureThreshold = 3
	defaultSuccessThreshold = 2
)

// defaultMaxConcurrentChecks bounds how many endpoints one CheckOnce
// pass probes at once, so a registry with a very large number of
// endpoints doesn't open an unbounded number of simultaneous
// connections. Not currently exposed as configuration — no real use
// case for tuning it has come up yet.
const defaultMaxConcurrentChecks = 50

// Monitor actively health-checks every endpoint of every service
// registered in a Registry, on a fixed interval, applying
// FailureThreshold/SuccessThreshold hysteresis before writing a
// HEALTHY/UNHEALTHY transition back to the registry — so one slow or
// flaky probe doesn't flap an endpoint's state.
//
// One Monitor applies one HealthCheckPolicy to every endpoint it
// checks. Resolving a different policy per service or per route is a
// Policy Engine concern (a later development phase); today's scope is
// the mechanism, not per-destination policy selection.
//
// See PassiveTracker for the complementary, request-outcome-driven
// signal: the two are independent observers of the same registry, each
// with their own hysteresis, not a single merged state machine (that
// unification is a later development phase — see the circuit breaker).
type Monitor struct {
	registry *registry.Registry
	checker  Checker
	tracker  *stateTracker

	interval time.Duration
	timeout  time.Duration
}

// NewMonitor builds a Monitor that checks every endpoint in reg using
// checker, according to policy. A nil policy — or any zero-valued field
// within one — falls back to built-in defaults (10s interval, 2s
// per-check timeout, 3 consecutive failures to eject, 2 consecutive
// successes to recover). logger must not be nil.
func NewMonitor(reg *registry.Registry, checker Checker, policy *meshv1alpha1.HealthCheckPolicy, logger *slog.Logger) *Monitor {
	interval, timeout := defaultInterval, defaultTimeout
	if policy != nil {
		if d := policy.GetInterval().AsDuration(); d > 0 {
			interval = d
		}
		if d := policy.GetTimeout().AsDuration(); d > 0 {
			timeout = d
		}
	}
	failureThreshold, successThreshold := thresholdsWithDefaults(policy)

	return &Monitor{
		registry: reg,
		checker:  checker,
		tracker:  newStateTracker(reg, logger, "active", failureThreshold, successThreshold),
		interval: interval,
		timeout:  timeout,
	}
}

// Run calls CheckOnce immediately, then again every configured interval,
// until ctx is canceled. It's meant to be run in its own goroutine,
// following the same context-cancellation shutdown pattern used by
// EdgeMesh's binaries (see cmd/edgemesh-proxy).
func (m *Monitor) Run(ctx context.Context) {
	m.CheckOnce(ctx)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.CheckOnce(ctx)
		}
	}
}

// CheckOnce runs a single health-check pass over every endpoint of
// every service currently in the registry, probing endpoints
// concurrently (bounded by defaultMaxConcurrentChecks), and writes any
// resulting HEALTHY/UNHEALTHY transition back to the registry. It
// returns once every probe has completed or ctx is canceled, whichever
// is first.
//
// An individual probe failure is an ordinary, expected outcome — not
// something CheckOnce reports as an error — since a failing probe is
// exactly what this method exists to detect and act on.
func (m *Monitor) CheckOnce(ctx context.Context) {
	sem := make(chan struct{}, defaultMaxConcurrentChecks)
	var wg sync.WaitGroup

	for _, svc := range m.registry.ListServices() {
		if ctx.Err() != nil {
			break
		}
		endpoints, err := m.registry.Lookup(svc.GetName())
		if err != nil {
			// The service was deregistered between ListServices and
			// Lookup; nothing to check anymore.
			continue
		}
		for _, ep := range endpoints {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				wg.Wait()
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				m.checkEndpoint(ctx, ep)
			}()
		}
	}

	wg.Wait()
}

// checkEndpoint probes one endpoint and hands the result to the shared
// tracker, which applies the consecutive-streak hysteresis and writes
// any resulting state transition to the registry.
func (m *Monitor) checkEndpoint(ctx context.Context, ep *meshv1alpha1.Endpoint) {
	checkCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	err := m.checker.Check(checkCtx, ep)
	m.tracker.record(ep, err)
}
