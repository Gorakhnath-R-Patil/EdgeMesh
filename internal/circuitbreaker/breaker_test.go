package circuitbreaker_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/circuitbreaker"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

func policy(failureThreshold, halfOpenMaxRequests uint32, recoveryTimeout time.Duration) *meshv1alpha1.CircuitBreakerPolicy {
	return &meshv1alpha1.CircuitBreakerPolicy{
		ConsecutiveFailureThreshold: failureThreshold,
		RecoveryTimeout:             durationpb.New(recoveryTimeout),
		HalfOpenMaxRequests:         halfOpenMaxRequests,
	}
}

// ---- State machine basics ----

func TestBreakerStartsClosedAndAllows(t *testing.T) {
	b := circuitbreaker.New(policy(3, 1, time.Second))
	if got := b.State(); got != circuitbreaker.StateClosed {
		t.Fatalf("State() = %v, want CLOSED", got)
	}
	if !b.Allow() {
		t.Fatal("Allow() = false, want true when CLOSED")
	}
}

func TestBreakerTripsAfterFailureThreshold(t *testing.T) {
	b := circuitbreaker.New(policy(3, 1, time.Second))

	for i := 1; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("Allow() = false before threshold reached (failure %d)", i)
		}
		b.Failure()
		if got := b.State(); got != circuitbreaker.StateClosed {
			t.Fatalf("after %d failure(s), State() = %v, want still CLOSED", i, got)
		}
	}

	if !b.Allow() {
		t.Fatal("Allow() = false, want true for the 3rd call")
	}
	b.Failure() // 3rd consecutive failure crosses the threshold

	if got := b.State(); got != circuitbreaker.StateOpen {
		t.Fatalf("after 3 consecutive failures, State() = %v, want OPEN", got)
	}
	if b.Allow() {
		t.Fatal("Allow() = true immediately after tripping OPEN, want false")
	}
}

func TestBreakerSuccessResetsFailureStreak(t *testing.T) {
	b := circuitbreaker.New(policy(3, 1, time.Second))

	b.Allow()
	b.Failure()
	b.Allow()
	b.Failure() // 2 consecutive failures, one short of the threshold

	b.Allow()
	b.Success() // resets the streak

	// Two more failures must not trip it: the streak was reset, so
	// this is only 2 consecutive failures again, not 4.
	b.Allow()
	b.Failure()
	b.Allow()
	b.Failure()

	if got := b.State(); got != circuitbreaker.StateClosed {
		t.Fatalf("State() = %v, want still CLOSED (failure streak was reset by the intervening success)", got)
	}
}

func TestBreakerStaysOpenBeforeRecoveryTimeout(t *testing.T) {
	b := circuitbreaker.New(policy(1, 1, time.Hour))
	b.Allow()
	b.Failure() // trips immediately (threshold 1)

	for i := 0; i < 3; i++ {
		if b.Allow() {
			t.Fatal("Allow() = true before the recovery timeout elapsed, want false")
		}
	}
	if got := b.State(); got != circuitbreaker.StateOpen {
		t.Fatalf("State() = %v, want still OPEN", got)
	}
}

func TestBreakerTransitionsToHalfOpenAfterRecoveryTimeout(t *testing.T) {
	const recovery = 20 * time.Millisecond
	b := circuitbreaker.New(policy(1, 1, recovery))
	b.Allow()
	b.Failure() // trips OPEN

	time.Sleep(recovery + 60*time.Millisecond)

	if !b.Allow() {
		t.Fatal("Allow() = false after the recovery timeout elapsed, want true (a trial probe)")
	}
	if got := b.State(); got != circuitbreaker.StateHalfOpen {
		t.Fatalf("State() = %v, want HALF_OPEN after the recovery timeout elapsed", got)
	}
}

func TestBreakerHalfOpenSuccessClosesAfterEnoughSuccesses(t *testing.T) {
	const recovery = 20 * time.Millisecond
	b := circuitbreaker.New(policy(1, 2, recovery)) // 2 trial successes needed to close
	b.Allow()
	b.Failure()
	time.Sleep(recovery + 60*time.Millisecond)

	if !b.Allow() {
		t.Fatal("Allow() = false, want true (1st trial)")
	}
	b.Success()
	if got := b.State(); got != circuitbreaker.StateHalfOpen {
		t.Fatalf("after 1 of 2 required trial successes, State() = %v, want still HALF_OPEN", got)
	}

	if !b.Allow() {
		t.Fatal("Allow() = false, want true (2nd trial)")
	}
	b.Success()
	if got := b.State(); got != circuitbreaker.StateClosed {
		t.Fatalf("after 2 of 2 required trial successes, State() = %v, want CLOSED", got)
	}
}

func TestBreakerHalfOpenFailureReopensImmediately(t *testing.T) {
	const recovery = 20 * time.Millisecond
	b := circuitbreaker.New(policy(1, 5, recovery)) // would need 5 successes to close
	b.Allow()
	b.Failure()
	time.Sleep(recovery + 60*time.Millisecond)

	if !b.Allow() {
		t.Fatal("Allow() = false, want true (trial probe)")
	}
	b.Failure() // a single failed trial, despite the threshold being 5

	if got := b.State(); got != circuitbreaker.StateOpen {
		t.Fatalf("after a failed trial probe, State() = %v, want OPEN immediately (no partial credit)", got)
	}
	if b.Allow() {
		t.Fatal("Allow() = true immediately after re-tripping, want false")
	}
}

func TestBreakerReopenedRestartsRecoveryTimer(t *testing.T) {
	const recovery = 30 * time.Millisecond
	b := circuitbreaker.New(policy(1, 1, recovery))
	b.Allow()
	b.Failure() // trip #1
	time.Sleep(recovery + 60*time.Millisecond)

	b.Allow()   // -> HALF_OPEN, admits the trial
	b.Failure() // trip #2, restarts the timer

	// Immediately after re-tripping, the (restarted) recovery timeout
	// has not elapsed yet.
	if b.Allow() {
		t.Fatal("Allow() = true immediately after re-tripping, want false (recovery timer must have restarted)")
	}

	time.Sleep(recovery + 60*time.Millisecond)
	if !b.Allow() {
		t.Fatal("Allow() = false after waiting out the restarted recovery timeout, want true")
	}
}

func TestBreakerHalfOpenLimitsInFlightProbes(t *testing.T) {
	const recovery = 20 * time.Millisecond
	b := circuitbreaker.New(policy(1, 1, recovery)) // only 1 trial at a time
	b.Allow()
	b.Failure()
	time.Sleep(recovery + 60*time.Millisecond)

	if !b.Allow() {
		t.Fatal("1st Allow() = false, want true")
	}
	if b.Allow() {
		t.Fatal("2nd concurrent Allow() = true, want false (halfOpenMaxRequests is 1 and a trial is already in flight)")
	}
}

func TestBreakerAppliesDefaultsWithNilPolicy(t *testing.T) {
	b := circuitbreaker.New(nil)
	// Default failure threshold is 5.
	for i := 0; i < 4; i++ {
		b.Allow()
		b.Failure()
	}
	if got := b.State(); got != circuitbreaker.StateClosed {
		t.Fatalf("after 4 failures with default policy, State() = %v, want still CLOSED", got)
	}
	b.Allow()
	b.Failure()
	if got := b.State(); got != circuitbreaker.StateOpen {
		t.Fatalf("after 5 failures with default policy, State() = %v, want OPEN", got)
	}
}

func TestStateString(t *testing.T) {
	cases := map[circuitbreaker.State]string{
		circuitbreaker.StateClosed:   "CLOSED",
		circuitbreaker.StateOpen:     "OPEN",
		circuitbreaker.StateHalfOpen: "HALF_OPEN",
		circuitbreaker.State(99):     "UNKNOWN",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", state, got, want)
		}
	}
}

// ---- Concurrency ----

func TestBreakerConcurrentRequestsNeverExceedHalfOpenLimit(t *testing.T) {
	const recovery = 20 * time.Millisecond
	const maxTrials = 3
	b := circuitbreaker.New(policy(1, maxTrials, recovery))
	b.Allow()
	b.Failure()
	time.Sleep(recovery + 60*time.Millisecond)

	const goroutines = 50
	var admitted int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if b.Allow() {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	wg.Wait()

	if admitted > maxTrials {
		t.Fatalf("admitted %d concurrent trial calls, want at most %d (HalfOpenMaxRequests)", admitted, maxTrials)
	}
	if admitted == 0 {
		t.Fatal("admitted 0 trial calls, want at least 1")
	}
}

func TestBreakerConcurrentAllowSuccessFailureIsRaceFree(t *testing.T) {
	b := circuitbreaker.New(policy(5, 3, 15*time.Millisecond))

	const goroutines = 20
	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if b.Allow() {
					// Deterministic-per-call mix of outcomes, no
					// sleeping, so this test runs fast while still
					// exercising every code path under contention.
					if (g+i)%3 == 0 {
						b.Failure()
					} else {
						b.Success()
					}
				}
			}
		}(g)
	}
	wg.Wait()

	// Reaching here without a panic/race (checked by `go test -race` in
	// CI) is the primary assertion. The breaker must also still report
	// one of the three valid states.
	switch b.State() {
	case circuitbreaker.StateClosed, circuitbreaker.StateOpen, circuitbreaker.StateHalfOpen:
	default:
		t.Fatalf("State() = %v, want one of CLOSED/OPEN/HALF_OPEN", b.State())
	}
}
