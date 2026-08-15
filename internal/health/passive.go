package health

import (
	"log/slog"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/registry"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// PassiveTracker turns real request outcomes into the same
// HEALTHY/UNHEALTHY signal Monitor produces from active probes, but
// driven by traffic the data plane is already sending rather than a
// synthetic, separately scheduled check. A future integration (once
// the proxy selects endpoints through the registry — see internal/lb
// and internal/registry) calls Observe once per proxied request, right
// after the outcome is known.
//
// PassiveTracker applies the same FailureThreshold/SuccessThreshold
// consecutive-streak hysteresis as Monitor, from the same
// HealthCheckPolicy message, so a single failed request never ejects
// an endpoint — a real production failure gets exactly as much benefit
// of the doubt as a failed active probe does, no more and no less.
//
// PassiveTracker keeps its own counters, independent of any Monitor
// watching the same registry. This is deliberate, not an oversight: an
// active probe and a live client request are different kinds of
// sample — mixing their streaks would let an active check's success
// paper over a real, ongoing production failure (or vice versa).
// Unifying every health signal into one authoritative per-endpoint
// state machine is the circuit breaker's job (a later development
// phase); today, active and passive checks are independent observers
// that both write to the same registry, converging on the same ground
// truth without being coordinated.
//
// Passive observation alone cannot detect recovery once an endpoint is
// excluded from routing (see FilterHealthy) — a routing layer that
// stops sending an UNHEALTHY endpoint traffic also stops generating
// observations for it. Recovery detection is Monitor's job.
type PassiveTracker struct {
	tracker *stateTracker
}

// NewPassiveTracker builds a PassiveTracker that updates reg according
// to policy. A nil policy — or any zero-valued threshold field within
// one — falls back to the same built-in defaults as NewMonitor (3
// consecutive failures to eject, 2 consecutive successes to reset the
// streak). Only the threshold fields of policy are used; Interval and
// Timeout are Monitor-specific (there is no schedule to run on here —
// Observe is called by the request path). logger must not be nil.
func NewPassiveTracker(reg *registry.Registry, policy *meshv1alpha1.HealthCheckPolicy, logger *slog.Logger) *PassiveTracker {
	failureThreshold, successThreshold := thresholdsWithDefaults(policy)
	return &PassiveTracker{
		tracker: newStateTracker(reg, logger, "passive", failureThreshold, successThreshold),
	}
}

// Observe records one real request's outcome against ep — a nil
// outcomeErr means the request succeeded, any non-nil outcomeErr means
// it failed. Callers classify the raw outcome (an HTTP status code, a
// transport error) with ClassifyHTTPStatus/ClassifyError before calling
// Observe, so PassiveTracker itself only ever deals with the resulting
// success/failure signal — the same shape Checker.Check already uses.
func (p *PassiveTracker) Observe(ep *meshv1alpha1.Endpoint, outcomeErr error) {
	p.tracker.record(ep, outcomeErr)
}
