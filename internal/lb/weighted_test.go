package lb_test

import (
	"math"
	"sync"
	"testing"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/lb"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// --- Statistical test methodology ---
//
// Weighted's selection is random, so no single call — or even a small
// batch of calls — is expected to land exactly on the configured
// proportions. What's testable is that, over many trials, the observed
// share for each endpoint converges to its configured share within the
// variance a fair weighted coin flip predicts.
//
// Each Next call for a two-way split is a Bernoulli trial with success
// probability p = weight / totalWeight. Across n independent trials,
// the number of successes follows a Binomial(n, p) distribution with
// standard deviation:
//
//	sigma = sqrt(n * p * (1-p))
//
// A test asserting the observed count falls within k*sigma of the
// expected count (n*p) has a false-failure rate bounded by the
// binomial tail at k standard deviations. This suite uses k=5, whose
// two-sided false-positive probability is under 6e-7 per assertion
// (by Chebyshev's inequality: at most 1/k^2 = 4%, and in practice, for
// a near-Normal binomial at these sample sizes, several orders of
// magnitude tighter) — negligible flakiness risk while still being a
// real statistical check, not a tautology.
const sigmaTolerance = 5.0

func binomialStdDev(n int, p float64) float64 {
	return math.Sqrt(float64(n) * p * (1 - p))
}

// assertWithinTolerance fails the test if observed count deviates from
// the expected count (n*wantP) by more than sigmaTolerance standard
// deviations.
func assertWithinTolerance(t *testing.T, label string, observed, n int, wantP float64) {
	t.Helper()
	expected := wantP * float64(n)
	tolerance := sigmaTolerance * binomialStdDev(n, wantP)
	diff := math.Abs(float64(observed) - expected)
	if diff > tolerance {
		t.Errorf("%s: observed %d/%d selections (share %.4f), want share %.4f (count %.0f +/- %.1f at %.0f sigma)",
			label, observed, n, float64(observed)/float64(n), wantP, expected, tolerance, sigmaTolerance)
	}
}

func weightedEndpoint(id string, weight uint32) *meshv1alpha1.Endpoint {
	return &meshv1alpha1.Endpoint{Id: id, ServiceName: "payment", Address: "10.0.0.1", Port: 8080, Weight: weight}
}

func runTrials(t *testing.T, b lb.Balancer, pool []*meshv1alpha1.Endpoint, n int) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(pool))
	for i := 0; i < n; i++ {
		ep, err := b.Next(pool)
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		counts[ep.GetId()]++
	}
	return counts
}

// ---- Statistical distribution ----

func TestWeightedTwoWaySplitMatchesConfiguredProportions(t *testing.T) {
	w := lb.NewWeighted()
	pool := []*meshv1alpha1.Endpoint{weightedEndpoint("v1", 90), weightedEndpoint("v2", 10)}

	const trials = 200_000
	counts := runTrials(t, w, pool, trials)

	assertWithinTolerance(t, "v1 (weight 90/100)", counts["v1"], trials, 0.90)
	assertWithinTolerance(t, "v2 (weight 10/100)", counts["v2"], trials, 0.10)
}

func TestWeightedThreeWaySplitMatchesConfiguredProportions(t *testing.T) {
	w := lb.NewWeighted()
	pool := []*meshv1alpha1.Endpoint{
		weightedEndpoint("v1", 70),
		weightedEndpoint("v2", 20),
		weightedEndpoint("v3", 10),
	}

	const trials = 200_000
	counts := runTrials(t, w, pool, trials)

	assertWithinTolerance(t, "v1 (weight 70/100)", counts["v1"], trials, 0.70)
	assertWithinTolerance(t, "v2 (weight 20/100)", counts["v2"], trials, 0.20)
	assertWithinTolerance(t, "v3 (weight 10/100)", counts["v3"], trials, 0.10)
}

func TestWeightedEqualWeightsDistributeUniformly(t *testing.T) {
	w := lb.NewWeighted()
	pool := []*meshv1alpha1.Endpoint{
		weightedEndpoint("a", 25),
		weightedEndpoint("b", 25),
		weightedEndpoint("c", 25),
		weightedEndpoint("d", 25),
	}

	const trials = 200_000
	counts := runTrials(t, w, pool, trials)

	for _, id := range []string{"a", "b", "c", "d"} {
		assertWithinTolerance(t, "endpoint "+id+" (equal weight)", counts[id], trials, 0.25)
	}
}

func TestWeightedZeroWeightUsesDefaultShare(t *testing.T) {
	w := lb.NewWeighted()
	// "v2" leaves Weight unset (0), which must be treated as
	// defaultWeight (1) -- the same as "v1" here -- producing a 50/50
	// split, not a 0% share for v2.
	pool := []*meshv1alpha1.Endpoint{weightedEndpoint("v1", 1), weightedEndpoint("v2", 0)}

	const trials = 200_000
	counts := runTrials(t, w, pool, trials)

	assertWithinTolerance(t, "v1 (explicit weight 1)", counts["v1"], trials, 0.50)
	assertWithinTolerance(t, "v2 (unset weight defaults to 1)", counts["v2"], trials, 0.50)
}

func TestWeightedAllZeroWeightsDistributeUniformly(t *testing.T) {
	w := lb.NewWeighted()
	pool := []*meshv1alpha1.Endpoint{weightedEndpoint("a", 0), weightedEndpoint("b", 0), weightedEndpoint("c", 0)}

	const trials = 150_000
	counts := runTrials(t, w, pool, trials)

	for _, id := range []string{"a", "b", "c"} {
		assertWithinTolerance(t, "endpoint "+id+" (all weights unset)", counts[id], trials, 1.0/3.0)
	}
}

// ---- Edge cases ----

func TestWeightedSingleEndpointAlwaysReturnsIt(t *testing.T) {
	w := lb.NewWeighted()
	pool := []*meshv1alpha1.Endpoint{weightedEndpoint("solo", 42)}

	for i := 0; i < 100; i++ {
		ep, err := w.Next(pool)
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		if ep.GetId() != "solo" {
			t.Fatalf("Next() = %q, want %q", ep.GetId(), "solo")
		}
	}
}

func TestWeightedEmptyPoolReturnsUnavailable(t *testing.T) {
	w := lb.NewWeighted()
	_, err := w.Next(nil)
	if !edgeerrors.Is(err, edgeerrors.ErrUnavailable) {
		t.Fatalf("error = %v, want it to wrap ErrUnavailable", err)
	}
}

func TestWeightedNeverSelectsOutsideThePool(t *testing.T) {
	w := lb.NewWeighted()
	pool := []*meshv1alpha1.Endpoint{weightedEndpoint("a", 1), weightedEndpoint("b", 1000)}

	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		ep, err := w.Next(pool)
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		seen[ep.GetId()] = true
	}
	for id := range seen {
		if id != "a" && id != "b" {
			t.Fatalf("Next() returned %q, which is not in the candidate pool", id)
		}
	}
}

// ---- Concurrency ----

func TestWeightedConcurrentNextIsRaceFreeAndStatisticallyCorrect(t *testing.T) {
	w := lb.NewWeighted()
	pool := []*meshv1alpha1.Endpoint{weightedEndpoint("v1", 90), weightedEndpoint("v2", 10)}

	const goroutines = 50
	const perGoroutine = 4000
	const trials = goroutines * perGoroutine

	var mu sync.Mutex
	counts := make(map[string]int, 2)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			local := make(map[string]int, 2)
			for i := 0; i < perGoroutine; i++ {
				ep, err := w.Next(pool)
				if err != nil {
					t.Errorf("Next() error = %v, want nil", err)
					return
				}
				local[ep.GetId()]++
			}
			mu.Lock()
			for id, c := range local {
				counts[id] += c
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	assertWithinTolerance(t, "v1 (concurrent)", counts["v1"], trials, 0.90)
	assertWithinTolerance(t, "v2 (concurrent)", counts["v2"], trials, 0.10)
}
