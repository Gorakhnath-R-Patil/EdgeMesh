package lb_test

import (
	"fmt"
	"sync"
	"testing"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/lb"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

func endpoint(id string) *meshv1alpha1.Endpoint {
	return &meshv1alpha1.Endpoint{Id: id, ServiceName: "payment", Address: "10.0.0.1", Port: 8080}
}

func endpoints(ids ...string) []*meshv1alpha1.Endpoint {
	eps := make([]*meshv1alpha1.Endpoint, len(ids))
	for i, id := range ids {
		eps[i] = endpoint(id)
	}
	return eps
}

func ids(eps []*meshv1alpha1.Endpoint) []string {
	out := make([]string, len(eps))
	for i, e := range eps {
		out[i] = e.GetId()
	}
	return out
}

// ---- Deterministic ----

func TestRoundRobinCyclesInOrder(t *testing.T) {
	r := lb.NewRoundRobin()
	pool := endpoints("A", "B", "C")

	var got []string
	for i := 0; i < 6; i++ {
		ep, err := r.Next(pool)
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		got = append(got, ep.GetId())
	}

	want := []string{"A", "B", "C", "A", "B", "C"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("selection sequence = %v, want %v", got, want)
	}
}

func TestRoundRobinSingleEndpointAlwaysReturnsIt(t *testing.T) {
	r := lb.NewRoundRobin()
	pool := endpoints("solo")

	for i := 0; i < 5; i++ {
		ep, err := r.Next(pool)
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		if ep.GetId() != "solo" {
			t.Fatalf("Next() = %q, want %q", ep.GetId(), "solo")
		}
	}
}

func TestRoundRobinEmptyPoolReturnsUnavailable(t *testing.T) {
	r := lb.NewRoundRobin()
	_, err := r.Next(nil)
	if !edgeerrors.Is(err, edgeerrors.ErrUnavailable) {
		t.Fatalf("error = %v, want it to wrap ErrUnavailable", err)
	}
}

func TestRoundRobinEmptySliceReturnsUnavailable(t *testing.T) {
	r := lb.NewRoundRobin()
	_, err := r.Next([]*meshv1alpha1.Endpoint{})
	if !edgeerrors.Is(err, edgeerrors.ErrUnavailable) {
		t.Fatalf("error = %v, want it to wrap ErrUnavailable", err)
	}
}

func TestRoundRobinIndependentInstancesHaveIndependentSequences(t *testing.T) {
	pool := endpoints("A", "B", "C")

	r1 := lb.NewRoundRobin()
	r2 := lb.NewRoundRobin()

	first1, _ := r1.Next(pool)
	first2, _ := r2.Next(pool)

	if first1.GetId() != "A" || first2.GetId() != "A" {
		t.Fatalf("two fresh RoundRobin instances = (%q, %q), want both to start at %q", first1.GetId(), first2.GetId(), "A")
	}
}

// ---- Endpoint removal / addition handling ----

func TestRoundRobinToleratesEndpointRemovalMidSequence(t *testing.T) {
	r := lb.NewRoundRobin()

	full := endpoints("A", "B", "C")
	if _, err := r.Next(full); err != nil { // consumes seq 0 -> A
		t.Fatalf("Next() error = %v, want nil", err)
	}

	reduced := endpoints("A", "C") // B removed from the registry
	for i := 0; i < 10; i++ {
		ep, err := r.Next(reduced)
		if err != nil {
			t.Fatalf("Next() error = %v, want nil after endpoint removal", err)
		}
		if ep.GetId() != "A" && ep.GetId() != "C" {
			t.Fatalf("Next() = %q, want one of the currently-registered endpoints (A, C)", ep.GetId())
		}
	}
}

func TestRoundRobinToleratesShrinkingToOneEndpoint(t *testing.T) {
	r := lb.NewRoundRobin()
	full := endpoints("A", "B", "C")
	for i := 0; i < 3; i++ {
		if _, err := r.Next(full); err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
	}

	solo := endpoints("A")
	for i := 0; i < 5; i++ {
		ep, err := r.Next(solo)
		if err != nil {
			t.Fatalf("Next() error = %v, want nil with a single surviving endpoint", err)
		}
		if ep.GetId() != "A" {
			t.Fatalf("Next() = %q, want %q", ep.GetId(), "A")
		}
	}
}

func TestRoundRobinRecoversWhenPoolBecomesEmptyThenRefills(t *testing.T) {
	r := lb.NewRoundRobin()
	pool := endpoints("A", "B")
	if _, err := r.Next(pool); err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}

	if _, err := r.Next(nil); !edgeerrors.Is(err, edgeerrors.ErrUnavailable) {
		t.Fatalf("Next(nil) error = %v, want ErrUnavailable", err)
	}

	// Once endpoints come back, selection must resume working (not
	// remain permanently broken because of the transient empty pool).
	ep, err := r.Next(pool)
	if err != nil {
		t.Fatalf("Next() after pool recovery error = %v, want nil", err)
	}
	if ep.GetId() != "A" && ep.GetId() != "B" {
		t.Fatalf("Next() = %q, want A or B", ep.GetId())
	}
}

func TestRoundRobinToleratesEndpointAdditionMidSequence(t *testing.T) {
	r := lb.NewRoundRobin()
	small := endpoints("A", "B")
	if _, err := r.Next(small); err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}

	grown := endpoints("A", "B", "C", "D")
	for i := 0; i < 8; i++ {
		ep, err := r.Next(grown)
		if err != nil {
			t.Fatalf("Next() error = %v, want nil after endpoint addition", err)
		}
		if id := ep.GetId(); id != "A" && id != "B" && id != "C" && id != "D" {
			t.Fatalf("Next() = %q, want one of A, B, C, D", id)
		}
	}
}

// ---- Concurrency ----

func TestRoundRobinConcurrentNextIsRaceFreeAndExactlyBalanced(t *testing.T) {
	r := lb.NewRoundRobin()
	pool := endpoints("A", "B", "C", "D")

	const goroutines = 50
	const perGoroutine = 400 // goroutines * perGoroutine is a multiple of len(pool)

	counts := make(map[string]*int64, len(pool))
	var mu sync.Mutex
	for _, ep := range pool {
		n := int64(0)
		counts[ep.GetId()] = &n
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				ep, err := r.Next(pool)
				if err != nil {
					t.Errorf("Next() error = %v, want nil", err)
					return
				}
				mu.Lock()
				*counts[ep.GetId()]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	total := int64(goroutines * perGoroutine)
	want := total / int64(len(pool))
	for id, count := range counts {
		if *count != want {
			t.Errorf("endpoint %q selected %d times, want exactly %d (total=%d, pool=%d): "+
				"the atomic sequence number each call receives is unique regardless of scheduling, "+
				"so distribution across a total that's a multiple of the pool size must be exact",
				id, *count, want, total, len(pool))
		}
	}
}

func TestRoundRobinConcurrentNextNeverPanicsOnChangingPool(t *testing.T) {
	r := lb.NewRoundRobin()

	var wg sync.WaitGroup
	wg.Add(2)

	stop := make(chan struct{})

	// One goroutine hammers Next with pools of varying, sometimes zero,
	// length -- this is the scenario a live registry produces under
	// churn (Day 4) -- and must never panic (index out of range) or
	// deadlock.
	go func() {
		defer wg.Done()
		pools := [][]*meshv1alpha1.Endpoint{
			nil,
			endpoints("A"),
			endpoints("A", "B"),
			endpoints("A", "B", "C"),
			endpoints("B", "C"),
		}
		for i := 0; i < 2000; i++ {
			_, _ = r.Next(pools[i%len(pools)]) // errors on the empty pool are expected and ignored
		}
		close(stop)
	}()

	go func() {
		defer wg.Done()
		pool := endpoints("X", "Y", "Z")
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = r.Next(pool)
			}
		}
	}()

	wg.Wait()
}
