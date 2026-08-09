// Package mesh validates EdgeMesh's core data models: Service,
// Endpoint, Route, Policy, HealthState, and RoutingDecision. The
// message shapes themselves are defined as protobuf in
// proto/edgemesh/mesh/v1alpha1 and generated into
// gen/go/edgemesh/mesh/v1alpha1 — this package is deliberately kept
// separate from that generated code (regenerated wholesale by `make
// proto-gen` and never hand-edited) so hand-written validation rules
// can evolve independently without risk of being overwritten.
//
// Validate* functions never mutate their argument and return an error
// wrapping internal/errors.ErrInvalidConfig listing every violation
// found, not just the first, so a caller (or an operator reading logs)
// sees the complete picture in one pass.
package mesh

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"google.golang.org/protobuf/types/known/durationpb"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// maxRetryAttempts bounds Policy.Retry.MaxAttempts. It exists here, at
// the data-model layer, so a config that would cause retry storms is
// rejected before it ever reaches the retry engine (a later development
// phase) — never allow uncontrolled retry behavior.
const maxRetryAttempts = 10

// nameRE matches DNS-1123 label-style names: lowercase alphanumeric and
// '-', 1-63 characters, starting and ending alphanumeric. EdgeMesh names
// (Service, Endpoint, Route, Policy) double as Kubernetes-compatible
// identifiers so later Kubernetes integration doesn't need a separate
// naming scheme or a translation layer.
var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func validateName(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s: must not be empty", field)
	}
	if !nameRE.MatchString(value) {
		return fmt.Errorf("%s: %q must be a lowercase RFC-1123 label (alphanumeric and '-', starting/ending alphanumeric, max 63 chars)", field, value)
	}
	return nil
}

// invalidConfig joins field-level violations into a single error
// wrapping ErrInvalidConfig, or nil if there were none.
func invalidConfig(op string, errs []string) error {
	if len(errs) == 0 {
		return nil
	}
	return edgeerrors.Wrap(op, fmt.Errorf("%w: %s", edgeerrors.ErrInvalidConfig, strings.Join(errs, "; ")))
}

// ValidateService rejects a Service with an invalid name/namespace or
// malformed ports.
func ValidateService(s *meshv1alpha1.Service) error {
	var errs []string

	if err := validateName("name", s.GetName()); err != nil {
		errs = append(errs, err.Error())
	}
	if ns := s.GetNamespace(); ns != "" {
		if err := validateName("namespace", ns); err != nil {
			errs = append(errs, err.Error())
		}
	}

	ports := s.GetPorts()
	seenNames := make(map[string]bool, len(ports))
	for i, p := range ports {
		if err := validateServicePort(p); err != nil {
			errs = append(errs, fmt.Sprintf("ports[%d]: %v", i, err))
			continue
		}
		if len(ports) > 1 {
			switch {
			case p.GetName() == "":
				errs = append(errs, fmt.Sprintf("ports[%d]: name is required when a service exposes more than one port", i))
			case seenNames[p.GetName()]:
				errs = append(errs, fmt.Sprintf("ports[%d]: duplicate port name %q", i, p.GetName()))
			default:
				seenNames[p.GetName()] = true
			}
		}
	}

	return invalidConfig("mesh.ValidateService", errs)
}

func validateServicePort(p *meshv1alpha1.ServicePort) error {
	if p == nil {
		return fmt.Errorf("must not be nil")
	}
	if p.GetPort() == 0 || p.GetPort() > 65535 {
		return fmt.Errorf("port: must be between 1 and 65535, got %d", p.GetPort())
	}
	return nil
}

// ValidateEndpoint rejects an Endpoint with an invalid id/service_name,
// a missing address, an out-of-range port, or an unrecognized health
// state.
func ValidateEndpoint(e *meshv1alpha1.Endpoint) error {
	var errs []string

	if err := validateName("id", e.GetId()); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateName("service_name", e.GetServiceName()); err != nil {
		errs = append(errs, err.Error())
	}

	switch addr := e.GetAddress(); {
	case strings.TrimSpace(addr) == "":
		errs = append(errs, "address: must not be empty")
	case strings.ContainsAny(addr, " \t\n"):
		errs = append(errs, fmt.Sprintf("address: must not contain whitespace, got %q", addr))
	}

	if e.GetPort() == 0 || e.GetPort() > 65535 {
		errs = append(errs, fmt.Sprintf("port: must be between 1 and 65535, got %d", e.GetPort()))
	}

	if !isValidHealthState(e.GetHealth()) {
		errs = append(errs, fmt.Sprintf("health: unknown HealthState value %d", e.GetHealth()))
	}

	return invalidConfig("mesh.ValidateEndpoint", errs)
}

// ValidateHealthState reports whether h is one of the enum's defined
// values. HEALTH_STATE_UNSPECIFIED is considered valid — it is the
// legitimate default for an endpoint that has not been checked yet, not
// an error condition.
func ValidateHealthState(h meshv1alpha1.HealthState) error {
	if !isValidHealthState(h) {
		return edgeerrors.Wrap("mesh.ValidateHealthState", fmt.Errorf("%w: unknown HealthState value %d", edgeerrors.ErrInvalidConfig, int32(h)))
	}
	return nil
}

func isValidHealthState(h meshv1alpha1.HealthState) bool {
	_, ok := meshv1alpha1.HealthState_name[int32(h)]
	return ok
}

// ValidateRoute rejects a Route with an invalid name, a malformed
// match, no destinations, or a malformed destination.
func ValidateRoute(r *meshv1alpha1.Route) error {
	var errs []string

	if err := validateName("name", r.GetName()); err != nil {
		errs = append(errs, err.Error())
	}

	if err := validateRouteMatch(r.GetMatch()); err != nil {
		errs = append(errs, fmt.Sprintf("match: %v", err))
	}

	dests := r.GetDestinations()
	if len(dests) == 0 {
		errs = append(errs, "destinations: at least one destination is required")
	}
	for i, d := range dests {
		if err := validateWeightedDestination(d); err != nil {
			errs = append(errs, fmt.Sprintf("destinations[%d]: %v", i, err))
		}
	}

	if pn := r.GetPolicyName(); pn != "" {
		if err := validateName("policy_name", pn); err != nil {
			errs = append(errs, err.Error())
		}
	}

	return invalidConfig("mesh.ValidateRoute", errs)
}

func validateRouteMatch(m *meshv1alpha1.RouteMatch) error {
	if m == nil {
		return nil // unset match = match every request
	}
	switch spec := m.GetPathSpecifier().(type) {
	case *meshv1alpha1.RouteMatch_PathExact:
		if !strings.HasPrefix(spec.PathExact, "/") {
			return fmt.Errorf("path_exact: must start with '/', got %q", spec.PathExact)
		}
	case *meshv1alpha1.RouteMatch_PathPrefix:
		if !strings.HasPrefix(spec.PathPrefix, "/") {
			return fmt.Errorf("path_prefix: must start with '/', got %q", spec.PathPrefix)
		}
	}
	return nil
}

func validateWeightedDestination(d *meshv1alpha1.WeightedDestination) error {
	if d == nil {
		return fmt.Errorf("must not be nil")
	}
	if err := validateName("service_name", d.GetServiceName()); err != nil {
		return err
	}
	if d.GetWeight() == 0 {
		return fmt.Errorf("weight: must be greater than 0")
	}
	return nil
}

// ValidatePolicy rejects a Policy with an invalid name, an unrecognized
// load-balancing strategy, non-positive configured durations, a retry
// budget that risks retry storms, or invalid retryable status codes.
func ValidatePolicy(p *meshv1alpha1.Policy) error {
	var errs []string

	if err := validateName("name", p.GetName()); err != nil {
		errs = append(errs, err.Error())
	}
	if !isValidLoadBalancingStrategy(p.GetLoadBalancing()) {
		errs = append(errs, fmt.Sprintf("load_balancing: unknown LoadBalancingStrategy value %d", p.GetLoadBalancing()))
	}

	if hc := p.GetHealthCheck(); hc != nil {
		if err := validateDurationField("health_check.interval", hc.GetInterval()); err != nil {
			errs = append(errs, err.Error())
		}
		if err := validateDurationField("health_check.timeout", hc.GetTimeout()); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if cb := p.GetCircuitBreaker(); cb != nil {
		if err := validateDurationField("circuit_breaker.recovery_timeout", cb.GetRecoveryTimeout()); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if rt := p.GetRetry(); rt != nil {
		if rt.GetMaxAttempts() > maxRetryAttempts {
			errs = append(errs, fmt.Sprintf("retry.max_attempts: must not exceed %d (uncontrolled retries risk retry storms), got %d", maxRetryAttempts, rt.GetMaxAttempts()))
		}
		if err := validateDurationField("retry.per_try_timeout", rt.GetPerTryTimeout()); err != nil {
			errs = append(errs, err.Error())
		}
		if err := validateDurationField("retry.backoff_base", rt.GetBackoffBase()); err != nil {
			errs = append(errs, err.Error())
		}
		if err := validateDurationField("retry.backoff_max", rt.GetBackoffMax()); err != nil {
			errs = append(errs, err.Error())
		}
		for _, code := range rt.GetRetryableStatusCodes() {
			if code < 100 || code > 599 {
				errs = append(errs, fmt.Sprintf("retry.retryable_status_codes: %d is not a valid HTTP status code", code))
			}
		}
	}

	if to := p.GetTimeout(); to != nil {
		if err := validateDurationField("timeout.request", to.GetRequest()); err != nil {
			errs = append(errs, err.Error())
		}
		if err := validateDurationField("timeout.idle", to.GetIdle()); err != nil {
			errs = append(errs, err.Error())
		}
	}

	return invalidConfig("mesh.ValidatePolicy", errs)
}

// validateDurationField accepts an unset duration (nil, meaning "not
// configured") but rejects a set duration that is malformed or not
// strictly positive — a zero or negative timeout/interval/backoff has
// no sane runtime meaning.
func validateDurationField(field string, d *durationpb.Duration) error {
	if d == nil {
		return nil
	}
	if err := d.CheckValid(); err != nil {
		return fmt.Errorf("%s: %v", field, err)
	}
	if d.AsDuration() <= 0 {
		return fmt.Errorf("%s: must be greater than 0, got %s", field, d.AsDuration())
	}
	return nil
}

func isValidLoadBalancingStrategy(s meshv1alpha1.LoadBalancingStrategy) bool {
	_, ok := meshv1alpha1.LoadBalancingStrategy_name[int32(s)]
	return ok
}

// ValidateRoutingDecision rejects a RoutingDecision missing its
// identifying fields, carrying an unrecognized strategy, or carrying a
// non-finite score.
func ValidateRoutingDecision(d *meshv1alpha1.RoutingDecision) error {
	var errs []string

	if strings.TrimSpace(d.GetRequestId()) == "" {
		errs = append(errs, "request_id: must not be empty")
	}
	if err := validateName("service_name", d.GetServiceName()); err != nil {
		errs = append(errs, err.Error())
	}
	if strings.TrimSpace(d.GetSelectedEndpointId()) == "" {
		errs = append(errs, "selected_endpoint_id: must not be empty")
	}
	if !isValidLoadBalancingStrategy(d.GetStrategy()) {
		errs = append(errs, fmt.Sprintf("strategy: unknown LoadBalancingStrategy value %d", d.GetStrategy()))
	}
	if score := d.GetScore(); math.IsNaN(score) || math.IsInf(score, 0) {
		errs = append(errs, fmt.Sprintf("score: must be a finite number, got %v", score))
	}

	return invalidConfig("mesh.ValidateRoutingDecision", errs)
}
