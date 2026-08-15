package health

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/registry"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// Built-in defaults, applied to any field left zero-valued in the
// HealthCheckPolicy passed to NewMonitor. A nil policy gets every
// default.
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
type Monitor struct {
	registry *registry.Registry
	checker  Checker
	logger   *slog.Logger

	interval         time.Duration
	timeout          time.Duration
	failureThreshold uint32
	successThreshold uint32

	mu     sync.Mutex
	counts map[string]*consecutiveCounts
}

// consecutiveCounts tracks one endpoint's current streak. Exactly one
// of the two fields is non-zero at a time: a success resets
// failures to 0 and vice versa.
type consecutiveCounts struct {
	failures  uint32
	successes uint32
}

// NewMonitor builds a Monitor that checks every endpoint in reg using
// checker, according to policy. A nil policy — or any zero-valued field
// within one — falls back to built-in defaults (10s interval, 2s
// per-check timeout, 3 consecutive failures to eject, 2 consecutive
// successes to recover). logger must not be nil.
func NewMonitor(reg *registry.Registry, checker Checker, policy *meshv1alpha1.HealthCheckPolicy, logger *slog.Logger) *Monitor {
	interval, timeout, failureThreshold, successThreshold := defaultInterval, defaultTimeout, uint32(defaultFailureThreshold), uint32(defaultSuccessThreshold)
	if policy != nil {
		if d := policy.GetInterval().AsDuration(); d > 0 {
			interval = d
		}
		if d := policy.GetTimeout().AsDuration(); d > 0 {
			timeout = d
		}
		if t := policy.GetFailureThreshold(); t > 0 {
			failureThreshold = t
		}
		if t := policy.GetSuccessThreshold(); t > 0 {
			successThreshold = t
		}
	}

	return &Monitor{
		registry:         reg,
		checker:          checker,
		logger:           logger,
		interval:         interval,
		timeout:          timeout,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		counts:           make(map[string]*consecutiveCounts),
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

// checkEndpoint probes one endpoint, updates its consecutive
// failure/success streak, and — if the streak just crossed the
// configured threshold — writes the resulting state transition to the
// registry.
func (m *Monitor) checkEndpoint(ctx context.Context, ep *meshv1alpha1.Endpoint) {
	checkCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	err := m.checker.Check(checkCtx, ep)

	key := ep.GetServiceName() + "/" + ep.GetId()

	m.mu.Lock()
	c, ok := m.counts[key]
	if !ok {
		c = &consecutiveCounts{}
		m.counts[key] = c
	}

	var newState meshv1alpha1.HealthState
	var transition bool
	if err == nil {
		c.failures = 0
		c.successes++
		if ep.GetHealth() != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY && c.successes >= m.successThreshold {
			newState, transition = meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY, true
		}
	} else {
		c.successes = 0
		c.failures++
		if ep.GetHealth() != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY && c.failures >= m.failureThreshold {
			newState, transition = meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY, true
		}
	}
	m.mu.Unlock()

	if !transition {
		return
	}

	updated := proto.Clone(ep).(*meshv1alpha1.Endpoint)
	updated.Health = newState
	if updateErr := m.registry.UpdateEndpoint(updated); updateErr != nil {
		// The endpoint or its service was deregistered concurrently;
		// nothing more to do.
		m.logger.Warn("health: could not apply state transition, endpoint no longer registered",
			"service", ep.GetServiceName(), "endpoint", ep.GetId(), "error", updateErr)
		return
	}

	attrs := []any{
		"service", ep.GetServiceName(),
		"endpoint", ep.GetId(),
		"from", ep.GetHealth().String(),
		"to", newState.String(),
	}
	if newState == meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		m.logger.Warn("health: endpoint marked unhealthy", append(attrs, "reason", err.Error())...)
	} else {
		m.logger.Info("health: endpoint recovered", attrs...)
	}
}
