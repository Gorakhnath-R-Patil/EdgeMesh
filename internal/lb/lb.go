// Package lb implements EdgeMesh's load-balancing strategies: the
// algorithms that pick one endpoint from a candidate set for a single
// request. Load balancing is deliberately independent from routing
// policy (which destination/subset a request matches — see the Route
// message in internal/mesh) and from health/circuit-breaker state
// (which endpoints are even eligible to be candidates) — both are
// later development phases. A Balancer only answers "given these
// candidates, which one?"
package lb

import (
	"fmt"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// Balancer selects one endpoint from a set of candidates for a single
// request. Implementations must be safe for concurrent use: the proxy
// calls Next once per request, from many request-handling goroutines
// at once.
//
// Next is not responsible for filtering candidates — health state,
// circuit-breaker state, and locality preferences (later development
// phases) are applied by the caller before the endpoints it passes in
// ever reach a Balancer. This keeps each Balancer implementation
// focused on one selection algorithm.
type Balancer interface {
	// Next returns one element of endpoints. It returns an error
	// wrapping internal/errors.ErrUnavailable if endpoints is empty —
	// there is nothing to select from, which is a normal, expected
	// runtime condition (e.g. every endpoint of a service is currently
	// down), not a programming error.
	Next(endpoints []*meshv1alpha1.Endpoint) (*meshv1alpha1.Endpoint, error)
}

// errNoEndpoints is returned (wrapped) by every Balancer implementation
// when given an empty candidate set, so callers can react uniformly
// with errors.Is(err, edgeerrors.ErrUnavailable) regardless of which
// strategy is configured.
func errNoEndpoints(op string) error {
	return edgeerrors.Wrap(op, fmt.Errorf("%w: no endpoints available", edgeerrors.ErrUnavailable))
}
