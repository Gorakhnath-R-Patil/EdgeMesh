package mesh_test

import (
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/durationpb"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/mesh"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

func wantInvalid(t *testing.T, err error) {
	t.Helper()
	if !edgeerrors.Is(err, edgeerrors.ErrInvalidConfig) {
		t.Fatalf("error = %v, want it to wrap ErrInvalidConfig", err)
	}
}

// ---- Service ----

func TestValidateServiceAcceptsMinimalService(t *testing.T) {
	err := mesh.ValidateService(&meshv1alpha1.Service{Name: "payment-service"})
	if err != nil {
		t.Fatalf("ValidateService() error = %v, want nil", err)
	}
}

func TestValidateServiceRejectsEmptyName(t *testing.T) {
	wantInvalid(t, mesh.ValidateService(&meshv1alpha1.Service{}))
}

func TestValidateServiceRejectsUppercaseName(t *testing.T) {
	wantInvalid(t, mesh.ValidateService(&meshv1alpha1.Service{Name: "Payment-Service"}))
}

func TestValidateServiceRejectsInvalidNamespace(t *testing.T) {
	wantInvalid(t, mesh.ValidateService(&meshv1alpha1.Service{Name: "payment", Namespace: "Not Valid"}))
}

func TestValidateServiceRejectsOutOfRangePort(t *testing.T) {
	svc := &meshv1alpha1.Service{
		Name:  "payment",
		Ports: []*meshv1alpha1.ServicePort{{Name: "http", Port: 70000}},
	}
	wantInvalid(t, mesh.ValidateService(svc))
}

func TestValidateServiceRequiresPortNameWhenMultiplePorts(t *testing.T) {
	svc := &meshv1alpha1.Service{
		Name: "payment",
		Ports: []*meshv1alpha1.ServicePort{
			{Name: "http", Port: 8080},
			{Port: 9090}, // missing name
		},
	}
	wantInvalid(t, mesh.ValidateService(svc))
}

func TestValidateServiceRejectsDuplicatePortNames(t *testing.T) {
	svc := &meshv1alpha1.Service{
		Name: "payment",
		Ports: []*meshv1alpha1.ServicePort{
			{Name: "http", Port: 8080},
			{Name: "http", Port: 8081},
		},
	}
	wantInvalid(t, mesh.ValidateService(svc))
}

func TestValidateServiceAcceptsSinglePortWithoutName(t *testing.T) {
	svc := &meshv1alpha1.Service{
		Name:  "payment",
		Ports: []*meshv1alpha1.ServicePort{{Port: 8080}},
	}
	if err := mesh.ValidateService(svc); err != nil {
		t.Fatalf("ValidateService() error = %v, want nil for a single unnamed port", err)
	}
}

func TestValidateServiceReportsAllViolations(t *testing.T) {
	err := mesh.ValidateService(&meshv1alpha1.Service{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error %q does not mention the name violation", err.Error())
	}
}

// ---- Endpoint ----

func validEndpoint() *meshv1alpha1.Endpoint {
	return &meshv1alpha1.Endpoint{
		Id:          "payment-1",
		ServiceName: "payment",
		Address:     "10.0.0.5",
		Port:        8080,
		Health:      meshv1alpha1.HealthState_HEALTH_STATE_HEALTHY,
	}
}

func TestValidateEndpointAcceptsWellFormedEndpoint(t *testing.T) {
	if err := mesh.ValidateEndpoint(validEndpoint()); err != nil {
		t.Fatalf("ValidateEndpoint() error = %v, want nil", err)
	}
}

func TestValidateEndpointAcceptsUnspecifiedHealth(t *testing.T) {
	e := validEndpoint()
	e.Health = meshv1alpha1.HealthState_HEALTH_STATE_UNSPECIFIED
	if err := mesh.ValidateEndpoint(e); err != nil {
		t.Fatalf("ValidateEndpoint() error = %v, want nil (unspecified health is a valid default)", err)
	}
}

func TestValidateEndpointRejectsEmptyID(t *testing.T) {
	e := validEndpoint()
	e.Id = ""
	wantInvalid(t, mesh.ValidateEndpoint(e))
}

func TestValidateEndpointRejectsEmptyAddress(t *testing.T) {
	e := validEndpoint()
	e.Address = ""
	wantInvalid(t, mesh.ValidateEndpoint(e))
}

func TestValidateEndpointRejectsWhitespaceInAddress(t *testing.T) {
	e := validEndpoint()
	e.Address = "10.0.0.5 "
	wantInvalid(t, mesh.ValidateEndpoint(e))
}

func TestValidateEndpointRejectsZeroPort(t *testing.T) {
	e := validEndpoint()
	e.Port = 0
	wantInvalid(t, mesh.ValidateEndpoint(e))
}

func TestValidateEndpointRejectsUnknownHealthState(t *testing.T) {
	e := validEndpoint()
	e.Health = meshv1alpha1.HealthState(99)
	wantInvalid(t, mesh.ValidateEndpoint(e))
}

func TestValidateHealthStateAcceptsAllDefinedValues(t *testing.T) {
	for val, name := range meshv1alpha1.HealthState_name {
		if err := mesh.ValidateHealthState(meshv1alpha1.HealthState(val)); err != nil {
			t.Errorf("ValidateHealthState(%s) error = %v, want nil", name, err)
		}
	}
}

func TestValidateHealthStateRejectsUnknownValue(t *testing.T) {
	wantInvalid(t, mesh.ValidateHealthState(meshv1alpha1.HealthState(1234)))
}

// ---- Route ----

func validRoute() *meshv1alpha1.Route {
	return &meshv1alpha1.Route{
		Name: "payment-route",
		Match: &meshv1alpha1.RouteMatch{
			PathSpecifier: &meshv1alpha1.RouteMatch_PathPrefix{PathPrefix: "/payment"},
		},
		Destinations: []*meshv1alpha1.WeightedDestination{
			{ServiceName: "payment", Weight: 100},
		},
	}
}

func TestValidateRouteAcceptsWellFormedRoute(t *testing.T) {
	if err := mesh.ValidateRoute(validRoute()); err != nil {
		t.Fatalf("ValidateRoute() error = %v, want nil", err)
	}
}

func TestValidateRouteAcceptsNilMatch(t *testing.T) {
	r := validRoute()
	r.Match = nil
	if err := mesh.ValidateRoute(r); err != nil {
		t.Fatalf("ValidateRoute() error = %v, want nil (nil match = match everything)", err)
	}
}

func TestValidateRouteRejectsPathPrefixWithoutLeadingSlash(t *testing.T) {
	r := validRoute()
	r.Match = &meshv1alpha1.RouteMatch{
		PathSpecifier: &meshv1alpha1.RouteMatch_PathPrefix{PathPrefix: "payment"},
	}
	wantInvalid(t, mesh.ValidateRoute(r))
}

func TestValidateRouteRejectsPathExactWithoutLeadingSlash(t *testing.T) {
	r := validRoute()
	r.Match = &meshv1alpha1.RouteMatch{
		PathSpecifier: &meshv1alpha1.RouteMatch_PathExact{PathExact: "payment"},
	}
	wantInvalid(t, mesh.ValidateRoute(r))
}

func TestValidateRouteRejectsNoDestinations(t *testing.T) {
	r := validRoute()
	r.Destinations = nil
	wantInvalid(t, mesh.ValidateRoute(r))
}

func TestValidateRouteRejectsZeroWeightDestination(t *testing.T) {
	r := validRoute()
	r.Destinations = []*meshv1alpha1.WeightedDestination{{ServiceName: "payment", Weight: 0}}
	wantInvalid(t, mesh.ValidateRoute(r))
}

func TestValidateRouteRejectsDestinationWithoutServiceName(t *testing.T) {
	r := validRoute()
	r.Destinations = []*meshv1alpha1.WeightedDestination{{Weight: 100}}
	wantInvalid(t, mesh.ValidateRoute(r))
}

func TestValidateRouteRejectsInvalidPolicyName(t *testing.T) {
	r := validRoute()
	r.PolicyName = "Not Valid"
	wantInvalid(t, mesh.ValidateRoute(r))
}

func TestValidateRouteAcceptsEmptyPolicyName(t *testing.T) {
	r := validRoute()
	r.PolicyName = ""
	if err := mesh.ValidateRoute(r); err != nil {
		t.Fatalf("ValidateRoute() error = %v, want nil (empty policy_name = use default policy)", err)
	}
}

// ---- Policy ----

func TestValidatePolicyAcceptsMinimalPolicy(t *testing.T) {
	err := mesh.ValidatePolicy(&meshv1alpha1.Policy{Name: "default"})
	if err != nil {
		t.Fatalf("ValidatePolicy() error = %v, want nil", err)
	}
}

func TestValidatePolicyRejectsEmptyName(t *testing.T) {
	wantInvalid(t, mesh.ValidatePolicy(&meshv1alpha1.Policy{}))
}

func TestValidatePolicyRejectsUnknownLoadBalancingStrategy(t *testing.T) {
	p := &meshv1alpha1.Policy{Name: "default", LoadBalancing: meshv1alpha1.LoadBalancingStrategy(99)}
	wantInvalid(t, mesh.ValidatePolicy(p))
}

func TestValidatePolicyRejectsExcessiveRetryAttempts(t *testing.T) {
	p := &meshv1alpha1.Policy{
		Name:  "default",
		Retry: &meshv1alpha1.RetryPolicy{MaxAttempts: 50},
	}
	wantInvalid(t, mesh.ValidatePolicy(p))
}

func TestValidatePolicyAcceptsReasonableRetryAttempts(t *testing.T) {
	p := &meshv1alpha1.Policy{
		Name:  "default",
		Retry: &meshv1alpha1.RetryPolicy{MaxAttempts: 3, PerTryTimeout: durationpb.New(500_000_000)},
	}
	if err := mesh.ValidatePolicy(p); err != nil {
		t.Fatalf("ValidatePolicy() error = %v, want nil", err)
	}
}

func TestValidatePolicyRejectsNonPositiveDuration(t *testing.T) {
	p := &meshv1alpha1.Policy{
		Name:    "default",
		Timeout: &meshv1alpha1.TimeoutPolicy{Request: durationpb.New(0)},
	}
	wantInvalid(t, mesh.ValidatePolicy(p))
}

func TestValidatePolicyRejectsNegativeDuration(t *testing.T) {
	p := &meshv1alpha1.Policy{
		Name:           "default",
		CircuitBreaker: &meshv1alpha1.CircuitBreakerPolicy{RecoveryTimeout: durationpb.New(-1_000_000_000)},
	}
	wantInvalid(t, mesh.ValidatePolicy(p))
}

func TestValidatePolicyAcceptsUnsetNestedDurations(t *testing.T) {
	p := &meshv1alpha1.Policy{
		Name:        "default",
		HealthCheck: &meshv1alpha1.HealthCheckPolicy{FailureThreshold: 5},
	}
	if err := mesh.ValidatePolicy(p); err != nil {
		t.Fatalf("ValidatePolicy() error = %v, want nil (unset duration = not configured)", err)
	}
}

func TestValidatePolicyRejectsInvalidRetryableStatusCode(t *testing.T) {
	p := &meshv1alpha1.Policy{
		Name:  "default",
		Retry: &meshv1alpha1.RetryPolicy{MaxAttempts: 3, RetryableStatusCodes: []uint32{503, 999}},
	}
	wantInvalid(t, mesh.ValidatePolicy(p))
}

// ---- RoutingDecision ----

func validRoutingDecision() *meshv1alpha1.RoutingDecision {
	return &meshv1alpha1.RoutingDecision{
		RequestId:          "req-1",
		ServiceName:        "payment",
		SelectedEndpointId: "payment-3",
		Strategy:           meshv1alpha1.LoadBalancingStrategy_LOAD_BALANCING_STRATEGY_ADAPTIVE,
		Reason:             "lowest adaptive score",
		Score:              0.82,
	}
}

func TestValidateRoutingDecisionAcceptsWellFormedDecision(t *testing.T) {
	if err := mesh.ValidateRoutingDecision(validRoutingDecision()); err != nil {
		t.Fatalf("ValidateRoutingDecision() error = %v, want nil", err)
	}
}

func TestValidateRoutingDecisionRejectsEmptyRequestID(t *testing.T) {
	d := validRoutingDecision()
	d.RequestId = ""
	wantInvalid(t, mesh.ValidateRoutingDecision(d))
}

func TestValidateRoutingDecisionRejectsEmptySelectedEndpoint(t *testing.T) {
	d := validRoutingDecision()
	d.SelectedEndpointId = ""
	wantInvalid(t, mesh.ValidateRoutingDecision(d))
}

func TestValidateRoutingDecisionRejectsUnknownStrategy(t *testing.T) {
	d := validRoutingDecision()
	d.Strategy = meshv1alpha1.LoadBalancingStrategy(99)
	wantInvalid(t, mesh.ValidateRoutingDecision(d))
}

func TestValidateRoutingDecisionRejectsNaNScore(t *testing.T) {
	d := validRoutingDecision()
	d.Score = math.NaN()
	wantInvalid(t, mesh.ValidateRoutingDecision(d))
}

func TestValidateRoutingDecisionRejectsInfiniteScore(t *testing.T) {
	d := validRoutingDecision()
	d.Score = math.Inf(1)
	wantInvalid(t, mesh.ValidateRoutingDecision(d))
}
