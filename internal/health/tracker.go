package health

import (
	"log/slog"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/registry"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// consecutiveCounts tracks one endpoint's current streak for a single
// observation source. Exactly one field is non-zero at a time: a
// success resets failures to 0, and a failure resets successes to 0.
type consecutiveCounts struct {
	failures  uint32
	successes uint32
}

// stateTracker is the consecutive-failure/success hysteresis and
// registry-update mechanism shared by Monitor (active checks) and
// PassiveTracker (passive, request-outcome-driven checks). Each keeps
// its own stateTracker with its own counters and its own threshold
// configuration — an active probe result and a passive request outcome
// for the same endpoint are independent signals, not merged into one
// streak. See PassiveTracker's doc comment for why that separation is
// deliberate.
type stateTracker struct {
	registry *registry.Registry
	logger   *slog.Logger
	source   string // included in log records: "active" or "passive"

	failureThreshold uint32
	successThreshold uint32

	mu     sync.Mutex
	counts map[string]*consecutiveCounts
}

func newStateTracker(reg *registry.Registry, logger *slog.Logger, source string, failureThreshold, successThreshold uint32) *stateTracker {
	return &stateTracker{
		registry:         reg,
		logger:           logger,
		source:           source,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		counts:           make(map[string]*consecutiveCounts),
	}
}

// record applies one observed outcome for ep — a nil outcomeErr means
// success, any non-nil outcomeErr means failure — updates ep's
// consecutive streak, and, if the streak just crossed the configured
// threshold, writes the resulting HEALTHY/UNHEALTHY transition back to
// the registry and logs it. A streak that hasn't yet crossed its
// threshold updates silently: that's the hysteresis that keeps a single
// blip from flapping an endpoint's state.
func (t *stateTracker) record(ep *meshv1alpha1.Endpoint, outcomeErr error) {
	key := ep.GetServiceName() + "/" + ep.GetId()

	t.mu.Lock()
	c, ok := t.counts[key]
	if !ok {
		c = &consecutiveCounts{}
		t.counts[key] = c
	}

	var newState meshv1alpha1.HealthState
	var transition bool
	if outcomeErr == nil {
		c.failures = 0
		c.successes++
		if ep.GetHealth() != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY && c.successes >= t.successThreshold {
			newState, transition = meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY, true
		}
	} else {
		c.successes = 0
		c.failures++
		if ep.GetHealth() != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY && c.failures >= t.failureThreshold {
			newState, transition = meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY, true
		}
	}
	t.mu.Unlock()

	if !transition {
		return
	}

	updated := proto.Clone(ep).(*meshv1alpha1.Endpoint)
	updated.Health = newState
	if updateErr := t.registry.UpdateEndpoint(updated); updateErr != nil {
		// The endpoint or its service was deregistered concurrently;
		// nothing more to do.
		t.logger.Warn("health: could not apply state transition, endpoint no longer registered",
			"source", t.source, "service", ep.GetServiceName(), "endpoint", ep.GetId(), "error", updateErr)
		return
	}

	attrs := []any{
		"source", t.source,
		"service", ep.GetServiceName(),
		"endpoint", ep.GetId(),
		"from", ep.GetHealth().String(),
		"to", newState.String(),
	}
	if newState == meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.logger.Warn("health: endpoint marked unhealthy", append(attrs, "reason", outcomeErr.Error())...)
	} else {
		t.logger.Info("health: endpoint recovered", attrs...)
	}
}

// thresholdsWithDefaults resolves failure/success thresholds from a
// possibly-nil HealthCheckPolicy, applying this package's defaults for
// a nil policy or any zero-valued field within one.
func thresholdsWithDefaults(policy *meshv1alpha1.HealthCheckPolicy) (failureThreshold, successThreshold uint32) {
	failureThreshold, successThreshold = defaultFailureThreshold, defaultSuccessThreshold
	if policy != nil {
		if t := policy.GetFailureThreshold(); t > 0 {
			failureThreshold = t
		}
		if t := policy.GetSuccessThreshold(); t > 0 {
			successThreshold = t
		}
	}
	return failureThreshold, successThreshold
}
