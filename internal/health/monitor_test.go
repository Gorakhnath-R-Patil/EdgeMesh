package health_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/config"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/health"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/logging"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/registry"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// testLoggerJSON returns a JSON-format logger writing to buf, for tests
// that assert on emitted log fields.
func testLoggerJSON(buf *bytes.Buffer) *slog.Logger {
	return logging.NewWithWriter(buf, config.LoggingConfig{Level: config.LevelDebug, Format: config.FormatJSON}, "test")
}

func newRegistryWithEndpoint(t *testing.T, service, id string, initial meshv1alpha1.HealthState) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.RegisterService(&meshv1alpha1.Service{Name: service}); err != nil {
		t.Fatalf("RegisterService() error = %v", err)
	}
	if err := reg.RegisterEndpoint(&meshv1alpha1.Endpoint{
		Id: id, ServiceName: service, Address: "10.0.0.1", Port: 8080, Health: initial,
	}); err != nil {
		t.Fatalf("RegisterEndpoint() error = %v", err)
	}
	return reg
}

func healthOf(t *testing.T, reg *registry.Registry, service, id string) meshv1alpha1.HealthState {
	t.Helper()
	eps, err := reg.Lookup(service)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	for _, ep := range eps {
		if ep.GetId() == id {
			return ep.GetHealth()
		}
	}
	t.Fatalf("endpoint %q not found in service %q", id, service)
	return meshv1alpha1.HealthState_HEALTH_STATE_UNSPECIFIED
}

// alwaysFail is a Checker that always reports the given error.
func alwaysFail(msg string) health.Checker {
	return health.CheckerFunc(func(context.Context, *meshv1alpha1.Endpoint) error {
		return fmt.Errorf("%s", msg)
	})
}

// alwaysPass is a Checker that always succeeds.
var alwaysPass = health.CheckerFunc(func(context.Context, *meshv1alpha1.Endpoint) error { return nil })

func testPolicy(failureThreshold, successThreshold uint32) *meshv1alpha1.HealthCheckPolicy {
	return &meshv1alpha1.HealthCheckPolicy{
		FailureThreshold: failureThreshold,
		SuccessThreshold: successThreshold,
		Interval:         durationpb.New(time.Hour), // Run() isn't used in most tests; irrelevant when calling CheckOnce directly
		Timeout:          durationpb.New(2 * time.Second),
	}
}

func discardLogger() *slog.Logger {
	return logging.NewWithWriter(new(bytes.Buffer), config.LoggingConfig{Level: config.LevelError, Format: config.FormatText}, "test")
}

func TestMonitorMarksEndpointUnhealthyAfterThreshold(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_UNSPECIFIED)
	m := health.NewMonitor(reg, alwaysFail("connection refused"), testPolicy(3, 2), discardLogger())

	for i := 1; i < 3; i++ {
		m.CheckOnce(context.Background())
		if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNSPECIFIED {
			t.Fatalf("after %d failing check(s), health = %v, want still UNSPECIFIED (threshold not reached)", i, got)
		}
	}

	m.CheckOnce(context.Background()) // 3rd consecutive failure crosses the threshold
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Fatalf("after 3 failing checks, health = %v, want UNHEALTHY", got)
	}
}

func TestMonitorRecoversEndpointAfterThreshold(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY)
	m := health.NewMonitor(reg, alwaysPass, testPolicy(3, 2), discardLogger())

	m.CheckOnce(context.Background()) // 1st consecutive success
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Fatalf("after 1 passing check, health = %v, want still UNHEALTHY (threshold is 2)", got)
	}

	m.CheckOnce(context.Background()) // 2nd consecutive success crosses the threshold
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Fatalf("after 2 passing checks, health = %v, want HEALTHY", got)
	}
}

func TestMonitorDoesNotFlapOnSingleFailureBelowThreshold(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)

	var shouldFail bool
	var mu sync.Mutex
	checker := health.CheckerFunc(func(context.Context, *meshv1alpha1.Endpoint) error {
		mu.Lock()
		defer mu.Unlock()
		if shouldFail {
			return fmt.Errorf("boom")
		}
		return nil
	})

	m := health.NewMonitor(reg, checker, testPolicy(2, 2), discardLogger())

	mu.Lock()
	shouldFail = true
	mu.Unlock()
	m.CheckOnce(context.Background()) // 1 failure, below threshold of 2
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Fatalf("after a single failure (threshold 2), health = %v, want still HEALTHY", got)
	}

	m.CheckOnce(context.Background()) // 2nd consecutive failure crosses the threshold
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Fatalf("after 2 consecutive failures, health = %v, want UNHEALTHY", got)
	}
}

func TestMonitorResetsStreakOnAlternatingOutcomes(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)

	var fail bool
	var mu sync.Mutex
	checker := health.CheckerFunc(func(context.Context, *meshv1alpha1.Endpoint) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return fmt.Errorf("boom")
		}
		return nil
	})
	m := health.NewMonitor(reg, checker, testPolicy(2, 2), discardLogger())

	// fail, pass, fail, pass, ... never two consecutive failures, so the
	// endpoint must never cross the failure threshold of 2.
	for i := 0; i < 6; i++ {
		mu.Lock()
		fail = i%2 == 0
		mu.Unlock()
		m.CheckOnce(context.Background())
	}

	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Fatalf("after alternating outcomes with no 2-in-a-row failure, health = %v, want still HEALTHY", got)
	}
}

func TestMonitorTracksMultipleEndpointsIndependently(t *testing.T) {
	reg := registry.New()
	mustRegisterService(t, reg, "payment")
	mustRegisterEndpoint(t, reg, "payment", "good", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	mustRegisterEndpoint(t, reg, "payment", "bad", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)

	checker := health.CheckerFunc(func(_ context.Context, ep *meshv1alpha1.Endpoint) error {
		if ep.GetId() == "bad" {
			return fmt.Errorf("boom")
		}
		return nil
	})
	m := health.NewMonitor(reg, checker, testPolicy(2, 2), discardLogger())

	m.CheckOnce(context.Background())
	m.CheckOnce(context.Background())

	if got := healthOf(t, reg, "payment", "good"); got != meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("good endpoint health = %v, want HEALTHY (never failed)", got)
	}
	if got := healthOf(t, reg, "payment", "bad"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("bad endpoint health = %v, want UNHEALTHY (failed twice)", got)
	}
}

func TestMonitorChecksAllRegisteredServices(t *testing.T) {
	reg := registry.New()
	mustRegisterService(t, reg, "payment")
	mustRegisterService(t, reg, "order")
	mustRegisterEndpoint(t, reg, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	mustRegisterEndpoint(t, reg, "order", "order-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)

	m := health.NewMonitor(reg, alwaysFail("boom"), testPolicy(1, 1), discardLogger())
	m.CheckOnce(context.Background())

	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("payment-1 health = %v, want UNHEALTHY", got)
	}
	if got := healthOf(t, reg, "order", "order-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("order-1 health = %v, want UNHEALTHY", got)
	}
}

func TestMonitorAppliesDefaultsWithNilPolicy(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_UNSPECIFIED)
	m := health.NewMonitor(reg, alwaysFail("boom"), nil, discardLogger())

	// Default failure threshold is 3.
	for i := 0; i < 2; i++ {
		m.CheckOnce(context.Background())
	}
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNSPECIFIED {
		t.Fatalf("after 2 failures with default policy, health = %v, want still UNSPECIFIED", got)
	}
	m.CheckOnce(context.Background())
	if got := healthOf(t, reg, "payment", "payment-1"); got != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Fatalf("after 3 failures with default policy, health = %v, want UNHEALTHY", got)
	}
}

func TestMonitorLogsTransitionWithReason(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_UNSPECIFIED)
	var buf bytes.Buffer
	m := health.NewMonitor(reg, alwaysFail("dial tcp: connection refused"), testPolicy(1, 1), testLoggerJSON(&buf))

	m.CheckOnce(context.Background())

	found := false
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not valid JSON: %v\nline: %s", err, line)
		}
		if record["endpoint"] == "payment-1" && record["to"] == "HEALTH_STATE_UNHEALTHY" {
			found = true
			if record["reason"] != "dial tcp: connection refused" {
				t.Errorf("reason = %v, want the checker's error message", record["reason"])
			}
		}
	}
	if !found {
		t.Fatalf("no log line recorded the transition to UNHEALTHY\noutput: %s", buf.String())
	}
}

func TestMonitorRunStopsOnContextCancellation(t *testing.T) {
	reg := newRegistryWithEndpoint(t, "payment", "payment-1", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)
	policy := testPolicy(3, 2)
	policy.Interval = durationpb.New(5 * time.Millisecond)

	m := health.NewMonitor(reg, alwaysPass, policy, discardLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return within 2s of its context being canceled")
	}
}

func TestMonitorConcurrentCheckOnceIsRaceFree(t *testing.T) {
	reg := registry.New()
	mustRegisterService(t, reg, "payment")
	for i := 0; i < 100; i++ {
		mustRegisterEndpoint(t, reg, "payment", fmt.Sprintf("payment-%d", i), meshv1alpha1.HealthState_HEALTH_STATE_UNSPECIFIED)
	}

	var n int
	var mu sync.Mutex
	checker := health.CheckerFunc(func(context.Context, *meshv1alpha1.Endpoint) error {
		mu.Lock()
		n++
		fail := n%2 == 0
		mu.Unlock()
		if fail {
			return fmt.Errorf("boom")
		}
		return nil
	})

	m := health.NewMonitor(reg, checker, testPolicy(2, 2), discardLogger())

	var wg sync.WaitGroup
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				m.CheckOnce(context.Background())
			}
		}()
	}
	wg.Wait()

	// Reaching here without a panic/race (checked by `go test -race` in
	// CI) is the primary assertion. Sanity-check the registry is still
	// well-formed.
	eps, err := reg.Lookup("payment")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(eps) != 100 {
		t.Fatalf("Lookup() returned %d endpoints, want 100", len(eps))
	}
}

func mustRegisterService(t *testing.T, reg *registry.Registry, name string) {
	t.Helper()
	if err := reg.RegisterService(&meshv1alpha1.Service{Name: name}); err != nil {
		t.Fatalf("RegisterService(%q) error = %v", name, err)
	}
}

func mustRegisterEndpoint(t *testing.T, reg *registry.Registry, service, id string, initial meshv1alpha1.HealthState) {
	t.Helper()
	if err := reg.RegisterEndpoint(&meshv1alpha1.Endpoint{
		Id: id, ServiceName: service, Address: "10.0.0.1", Port: 8080, Health: initial,
	}); err != nil {
		t.Fatalf("RegisterEndpoint(%q) error = %v", id, err)
	}
}
