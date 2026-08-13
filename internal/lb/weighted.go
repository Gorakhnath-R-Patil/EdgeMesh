package lb

import (
	"math/rand/v2"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// defaultWeight applies to any endpoint whose Weight is 0 — "use the
// routing engine's default weight" per the Endpoint message's doc
// comment. Weights are relative to each other, not absolute
// percentages, so any positive constant is a valid default; 1 is the
// simplest "equal share" unit and matches Envoy's default endpoint
// weight.
const defaultWeight = 1

// Weighted selects endpoints at random, in proportion to their
// relative Weight: given endpoints weighted 90 and 10, the first is
// selected roughly nine times as often as the second, on average.
//
// Unlike RoundRobin, a single call to Next is not predictable — only
// the distribution over many calls converges to the configured
// proportions. See weighted_test.go for the statistical tolerance this
// is verified against and the standard-deviation math behind it.
//
// The zero value is ready to use. Weighted is safe for concurrent use:
// math/rand/v2's top-level generator is.
type Weighted struct{}

var _ Balancer = Weighted{}

// NewWeighted returns a ready-to-use Weighted balancer.
func NewWeighted() Weighted {
	return Weighted{}
}

// Next implements Balancer.
func (Weighted) Next(endpoints []*meshv1alpha1.Endpoint) (*meshv1alpha1.Endpoint, error) {
	if len(endpoints) == 0 {
		return nil, errNoEndpoints("lb.Weighted.Next")
	}

	total := 0
	for _, ep := range endpoints {
		total += effectiveWeight(ep)
	}

	// total >= len(endpoints) >= 1 here, since every endpoint
	// contributes at least defaultWeight, so rand.IntN(total) is safe.
	r := rand.IntN(total)
	for _, ep := range endpoints {
		w := effectiveWeight(ep)
		if r < w {
			return ep, nil
		}
		r -= w
	}

	// Unreachable: r < total by construction (rand.IntN's contract),
	// and total is the exact sum of the weights just walked, so the
	// loop above always returns before falling through.
	panic("lb.Weighted.Next: unreachable")
}

// effectiveWeight returns ep's Weight, or defaultWeight if it's unset
// (0).
func effectiveWeight(ep *meshv1alpha1.Endpoint) int {
	if w := ep.GetWeight(); w > 0 {
		return int(w)
	}
	return defaultWeight
}
