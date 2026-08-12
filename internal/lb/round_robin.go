package lb

import (
	"sync/atomic"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// RoundRobin selects endpoints in rotation: given [A, B, C], successive
// calls to Next return A, B, C, A, B, C, ... A RoundRobin holds exactly
// one rotation sequence; a caller that needs independent rotation per
// destination (e.g. one sequence per service) constructs one RoundRobin
// per destination rather than sharing a single instance across them.
//
// Next takes the candidate list fresh on every call instead of
// RoundRobin owning a copy of it, so it naturally tolerates the set
// changing between calls — endpoints added or removed by the registry
// (see internal/registry) between one request and the next. Each call
// simply indexes into whatever list it's given, modulo that list's
// current length; there is no per-endpoint identity to lose track of
// when the set changes.
//
// The zero value is ready to use. RoundRobin is safe for concurrent
// use: selection is a single atomic increment, no locking.
type RoundRobin struct {
	counter atomic.Uint64
}

var _ Balancer = (*RoundRobin)(nil)

// NewRoundRobin returns a ready-to-use RoundRobin.
func NewRoundRobin() *RoundRobin {
	return &RoundRobin{}
}

// Next implements Balancer.
func (r *RoundRobin) Next(endpoints []*meshv1alpha1.Endpoint) (*meshv1alpha1.Endpoint, error) {
	if len(endpoints) == 0 {
		return nil, errNoEndpoints("lb.RoundRobin.Next")
	}
	// Add returns the post-increment value, so subtract 1 to get a
	// 0-based sequence (0, 1, 2, ...). Every call across every
	// concurrent goroutine receives a distinct value from this
	// sequence — atomic.Uint64.Add is linearizable — so the resulting
	// index sequence is a true round-robin regardless of goroutine
	// scheduling, not merely an approximate one.
	seq := r.counter.Add(1) - 1
	return endpoints[seq%uint64(len(endpoints))], nil
}
