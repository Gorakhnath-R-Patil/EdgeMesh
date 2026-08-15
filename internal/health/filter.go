package health

import meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"

// FilterHealthy returns the subset of endpoints that are safe for
// normal routing — this is the concrete mechanism behind "remove
// unhealthy endpoints from normal routing": a caller (e.g. a future
// integration between the registry and a Balancer) filters a
// registry.Lookup result through this before handing it to a Balancer.
//
// Only HEALTH_STATE_UNHEALTHY is excluded. Endpoints with no health
// information yet (HEALTH_STATE_UNSPECIFIED — not yet checked) and any
// DEGRADED/RECOVERING endpoints are treated as routable: this package
// only actively manages the HEALTHY/UNHEALTHY transition (see Monitor);
// DEGRADED and RECOVERING are reserved for passive health signals, a
// later development phase, and are left untouched here rather than
// given behavior this package doesn't own.
//
// The returned slice shares its Endpoint pointers with endpoints; it
// does not copy them.
func FilterHealthy(endpoints []*meshv1alpha1.Endpoint) []*meshv1alpha1.Endpoint {
	out := make([]*meshv1alpha1.Endpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		if ep.GetHealth() != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
			out = append(out, ep)
		}
	}
	return out
}
