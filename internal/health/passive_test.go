package health_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/health"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/registry"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

func lookupOne(t *testing.T, reg *registry.Registry, service, id string) *meshv1alpha1.Endpoint {
	t.Helper()
	eps, err := reg.Lookup(service)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	for _, ep := range eps {
		if ep.GetId() == id {
			return ep
		}
	}
	t.Fatalf("endpoint %q not found in service %q", id, service)
	return nil
}

func TestPassiveTrackerEjectsAfterConsecutiveFailures(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	p := health.NewPassiveTracker(reg, testPolicy(3, 2), discardLogger())

	ep := lookupOne(t, reg, "payment", "payment-1")

	for i := 1; i < 3; i++ {
		p.Observe(ep, health.ClassifyHTTPStatus(503))
		if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
			t.Fatalf("after %d failed request(s), health = %v, want still HEALTHY (threshold not reached)", i, got)
		}
	}

	p.Observe(ep, health.ClassifyHTTPStatus(503)) // 3rd consecutive failure
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Fatalf("after 3 failed requests, health = %v, want UNHEALTHY", got)
	}
}

func TestPassiveTrackerDoesNotEjectOnSingleFailure(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	p := health.NewPassiveTracker(reg, testPolicy(3, 2), discardLogger())
	ep := lookupOne(t, reg, "payment", "payment-1")

	p.Observe(ep, health.ClassifyError(fmt.Errorf("connection reset by peer")))

	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Fatalf("after a single failed request (threshold 3), health = %v, want still HEALTHY", got)
	}
}

func TestPassiveTrackerIgnores4xxResponses(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	p := health.NewPassiveTracker(reg, testPolicy(1, 1), discardLogger())
	ep := lookupOne(t, reg, "payment", "payment-1")

	for i := 0; i < 5; i++ {
		p.Observe(ep, health.ClassifyHTTPStatus(404))
	}

	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Fatalf("after repeated 404 responses, health = %v, want still HEALTHY (4xx is not a backend health signal)", got)
	}
}

func TestPassiveTrackerIgnoresClientCancellation(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	p := health.NewPassiveTracker(reg, testPolicy(1, 1), discardLogger())
	ep := lookupOne(t, reg, "payment", "payment-1")

	p.Observe(ep, health.ClassifyError(context.Canceled))

	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Fatalf("after a client-canceled request, health = %v, want still HEALTHY", got)
	}
}

func TestPassiveTrackerNetworkFailureEjectsAfterThreshold(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	p := health.NewPassiveTracker(reg, testPolicy(2, 2), discardLogger())
	ep := lookupOne(t, reg, "payment", "payment-1")

	p.Observe(ep, health.ClassifyError(fmt.Errorf("dial tcp: connection refused")))
	p.Observe(ep, health.ClassifyError(fmt.Errorf("dial tcp: connection refused")))

	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Fatalf("after 2 consecutive network failures, health = %v, want UNHEALTHY", got)
	}
}

func TestPassiveTrackerTimeoutEjectsAfterThreshold(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	p := health.NewPassiveTracker(reg, testPolicy(1, 1), discardLogger())
	ep := lookupOne(t, reg, "payment", "payment-1")

	p.Observe(ep, health.ClassifyError(context.DeadlineExceeded))

	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Fatalf("after a request timeout, health = %v, want UNHEALTHY", got)
	}
}

func TestPassiveTrackerSuccessResetsStreak(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	p := health.NewPassiveTracker(reg, testPolicy(2, 2), discardLogger())
	ep := lookupOne(t, reg, "payment", "payment-1")

	p.Observe(ep, health.ClassifyHTTPStatus(503)) // failure 1
	p.Observe(ep, health.ClassifyHTTPStatus(200)) // success resets the streak
	p.Observe(ep, health.ClassifyHTTPStatus(503)) // failure 1 again, not 2 -- must not eject

	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Fatalf("after fail, success, fail (threshold 2 consecutive), health = %v, want still HEALTHY", got)
	}
}

func TestPassiveTrackerAndMonitorHaveIndependentStreaks(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	policy := testPolicy(2, 2)

	passive := health.NewPassiveTracker(reg, policy, discardLogger())
	monitor := health.NewMonitor(reg, alwaysPass, policy, discardLogger())

	ep := lookupOne(t, reg, "payment", "payment-1")

	// One passive failure...
	passive.Observe(ep, health.ClassifyHTTPStatus(503))
	// ...followed by an active check success must not erase the
	// passive failure streak (they're independent trackers), and must
	// not itself eject anything.
	monitor.CheckOnce(context.Background())
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Fatalf("after 1 passive failure + 1 active success, health = %v, want still HEALTHY", got)
	}

	// A second passive failure completes the passive streak
	// independently of the active checker's successes.
	passive.Observe(ep, health.ClassifyHTTPStatus(503))
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Fatalf("after 2 consecutive passive failures, health = %v, want UNHEALTHY even though the active checker keeps succeeding", got)
	}
}

func TestPassiveTrackerAppliesDefaultsWithNilPolicy(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	p := health.NewPassiveTracker(reg, nil, discardLogger())
	ep := lookupOne(t, reg, "payment", "payment-1")

	// Default failure threshold is 3.
	for i := 0; i < 2; i++ {
		p.Observe(ep, health.ClassifyHTTPStatus(500))
	}
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Fatalf("after 2 failures with default policy, health = %v, want still HEALTHY", got)
	}
	p.Observe(ep, health.ClassifyHTTPStatus(500))
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Fatalf("after 3 failures with default policy, health = %v, want UNHEALTHY", got)
	}
}

func TestPassiveTrackerConcurrentObserveIsRaceFree(t *testing.T) {
	reg := registry.New()
	mustRegisterService(t, reg, "payment")
	for i := 0; i < 50; i++ {
		mustRegisterEndpoint(t, reg, "payment", fmt.Sprintf("payment-%d", i), meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	}
	eps, err := reg.Lookup("payment")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	p := health.NewPassiveTracker(reg, testPolicy(3, 2), discardLogger())

	var wg sync.WaitGroup
	for i, ep := range eps {
		wg.Add(1)
		go func(i int, ep *meshv1alpha1.Endpoint) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if (i+j)%2 == 0 {
					p.Observe(ep, health.ClassifyHTTPStatus(503))
				} else {
					p.Observe(ep, health.ClassifyHTTPStatus(200))
				}
			}
		}(i, ep)
	}
	wg.Wait()

	// Reaching here without a panic/race (checked by `go test -race` in
	// CI) is the primary assertion.
	after, err := reg.Lookup("payment")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(after) != 50 {
		t.Fatalf("Lookup() returned %d endpoints, want 50", len(after))
	}
}
