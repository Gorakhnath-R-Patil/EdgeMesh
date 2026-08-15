package health_test

import (
	"testing"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/health"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

func stateEndpoint(id string, state meshv1alpha1.HealthState) *meshv1alpha1.Endpoint {
	return &meshv1alpha1.Endpoint{Id: id, ServiceName: "svc", Address: "10.0.0.1", Port: 8080, Health: state}
}

func TestFilterHealthyExcludesOnlyUnhealthy(t *testing.T) {
	in := []*meshv1alpha1.Endpoint{
		stateEndpoint("healthy", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY),
		stateEndpoint("unhealthy", meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY),
		stateEndpoint("unspecified", meshv1alpha1.HealthState_HEALTH_STATE_UNSPECIFIED),
		stateEndpoint("degraded", meshv1alpha1.HealthState_HEALTH_STATE_DEGRADED),
		stateEndpoint("recovering", meshv1alpha1.HealthState_HEALTH_STATE_RECOVERING),
	}

	out := health.FilterHealthy(in)

	want := map[string]bool{"healthy": true, "unspecified": true, "degraded": true, "recovering": true}
	if len(out) != len(want) {
		t.Fatalf("FilterHealthy() returned %d endpoints, want %d", len(out), len(want))
	}
	for _, ep := range out {
		if !want[ep.GetId()] {
			t.Errorf("FilterHealthy() unexpectedly included %q", ep.GetId())
		}
		if ep.GetId() == "unhealthy" {
			t.Error("FilterHealthy() included an UNHEALTHY endpoint")
		}
	}
}

func TestFilterHealthyEmptyInput(t *testing.T) {
	out := health.FilterHealthy(nil)
	if len(out) != 0 {
		t.Errorf("FilterHealthy(nil) = %v, want empty", out)
	}
}

func TestFilterHealthyAllUnhealthyReturnsEmpty(t *testing.T) {
	in := []*meshv1alpha1.Endpoint{
		stateEndpoint("a", meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY),
		stateEndpoint("b", meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY),
	}
	out := health.FilterHealthy(in)
	if len(out) != 0 {
		t.Errorf("FilterHealthy() = %v, want empty when every endpoint is unhealthy", out)
	}
}

func TestFilterHealthySharesPointersRatherThanCopying(t *testing.T) {
	in := []*meshv1alpha1.Endpoint{stateEndpoint("a", meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY)}
	out := health.FilterHealthy(in)
	if len(out) != 1 || out[0] != in[0] {
		t.Error("FilterHealthy() did not return the same pointer as the input")
	}
}
