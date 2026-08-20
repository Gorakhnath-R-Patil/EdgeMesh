// Package circuitbreaker implements EdgeMesh's circuit breaker: the
// CLOSED -> OPEN -> HALF_OPEN state machine that stops sending calls to
// a destination that's failing consistently, then cautiously probes it
// to find out when it's safe to resume.
//
// A circuit breaker is a different mechanism from internal/health,
// deliberately: health state (HEALTHY/UNHEALTHY, from active probes and
// passive observation) describes what the health engine believes about
// an endpoint's condition, for routing eligibility. A Breaker instead
// protects the caller from a known-bad destination in the moment,
// independent of whatever the health engine currently believes — it
// fails fast on the request path itself, with its own state and its
// own thresholds. The two are not unified here; that reconciliation, if
// it happens, is a later development phase.
package circuitbreaker

import (
	"sync"
	"time"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// State is a Breaker's position in the CLOSED -> OPEN -> HALF_OPEN
// state machine.
type State int

const (
	// StateClosed is the normal state: calls are allowed through, and
	// failures accumulate toward the trip threshold.
	StateClosed State = iota
	// StateOpen rejects every call until the recovery timeout elapses.
	StateOpen
	// StateHalfOpen allows a bounded number of trial calls through, to
	// find out whether the destination has recovered.
	StateHalfOpen
)

// String implements fmt.Stringer.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// Built-in defaults, applied to any field left zero-valued in the
// CircuitBreakerPolicy passed to New. A nil policy gets every default.
const (
	defaultFailureThreshold    = 5
	defaultRecoveryTimeout     = 30 * time.Second
	defaultHalfOpenMaxRequests = 1
)

// Breaker guards calls to a single destination (e.g. one endpoint).
// Callers ask Allow before attempting a call, then report the outcome
// via Success or Failure once it completes. A Breaker governs exactly
// one destination; a caller protecting several destinations constructs
// one Breaker per destination — the same per-destination convention
// lb.RoundRobin uses for its rotation state.
//
// Breaker never runs a background timer. The OPEN -> HALF_OPEN
// transition is evaluated lazily, the moment a caller calls Allow, by
// comparing the configured recovery timeout against how long the
// breaker has been open. State reports the state as of the last Allow
// call rather than forcing that evaluation itself, so merely inspecting
// a Breaker never has the side effect of transitioning it.
//
// While HALF_OPEN, up to HalfOpenMaxRequests trial calls are admitted
// concurrently; that same count is also how many consecutive successes
// are required to fully close — the standard "let N calls through and
// judge by their outcome" design (matching CircuitBreakerPolicy's
// single half_open_max_requests field, which does not distinguish the
// two). A single failure among them reopens the breaker immediately,
// discarding any successes seen so far in that probing round: recovery
// must look completely clean, not just mostly clean.
//
// Breaker is safe for concurrent use. Outcomes reported near a state
// transition are attributed to whatever state the breaker is in at the
// moment Success/Failure is called, not the state at the matching
// Allow call — a small, deliberately accepted raciness at state
// boundaries (see the concurrency tests) rather than the added
// complexity of per-call tracking tokens.
type Breaker struct {
	mu sync.Mutex

	state    State
	openedAt time.Time

	consecutiveFailures uint32 // counted while CLOSED
	halfOpenSuccesses   uint32 // counted while HALF_OPEN
	halfOpenInFlight    uint32 // trial calls currently admitted while HALF_OPEN

	failureThreshold    uint32
	recoveryTimeout     time.Duration
	halfOpenMaxRequests uint32
}

// New builds a Breaker from policy, starting CLOSED. A nil policy — or
// any zero-valued field within one — falls back to built-in defaults:
// 5 consecutive failures to trip, a 30s recovery timeout, and 1 trial
// request permitted while half-open (the most conservative choice: a
// single probe against a destination that might still be failing).
func New(policy *meshv1alpha1.CircuitBreakerPolicy) *Breaker {
	failureThreshold := uint32(defaultFailureThreshold)
	recoveryTimeout := time.Duration(defaultRecoveryTimeout)
	halfOpenMaxRequests := uint32(defaultHalfOpenMaxRequests)
	if policy != nil {
		if t := policy.GetConsecutiveFailureThreshold(); t > 0 {
			failureThreshold = t
		}
		if d := policy.GetRecoveryTimeout().AsDuration(); d > 0 {
			recoveryTimeout = d
		}
		if n := policy.GetHalfOpenMaxRequests(); n > 0 {
			halfOpenMaxRequests = n
		}
	}
	return &Breaker{
		state:               StateClosed,
		failureThreshold:    failureThreshold,
		recoveryTimeout:     recoveryTimeout,
		halfOpenMaxRequests: halfOpenMaxRequests,
	}
}

// State reports the breaker's state as of the last Allow call.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Allow reports whether a new call to the guarded destination may
// proceed right now. If the breaker is OPEN and the recovery timeout
// has elapsed, Allow first transitions it to HALF_OPEN before deciding.
//
// Every call for which Allow returns true must be matched by exactly
// one later call to Success or Failure once that call completes —
// that's what drives the breaker's next transition. A call for which
// Allow returns false must not be attempted at all; the breaker is
// telling the caller to fail fast instead.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true

	case StateOpen:
		if time.Since(b.openedAt) < b.recoveryTimeout {
			return false
		}
		b.state = StateHalfOpen
		b.halfOpenSuccesses = 0
		b.halfOpenInFlight = 0
		fallthrough

	case StateHalfOpen:
		if b.halfOpenInFlight >= b.halfOpenMaxRequests {
			return false
		}
		b.halfOpenInFlight++
		return true
	}

	return false
}

// Success reports that a call Allow let through succeeded.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.consecutiveFailures = 0

	case StateHalfOpen:
		b.halfOpenInFlight--
		b.halfOpenSuccesses++
		if b.halfOpenSuccesses >= b.halfOpenMaxRequests {
			b.close()
		}
	}
}

// Failure reports that a call Allow let through failed.
func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.consecutiveFailures++
		if b.consecutiveFailures >= b.failureThreshold {
			b.trip()
		}

	case StateHalfOpen:
		b.halfOpenInFlight--
		// Any failure while probing reopens the breaker immediately,
		// discarding whatever successes this probing round had seen.
		b.trip()
	}
}

// trip transitions to OPEN and (re)starts the recovery timer. Callers
// must hold b.mu.
func (b *Breaker) trip() {
	b.state = StateOpen
	b.openedAt = time.Now()
	b.consecutiveFailures = 0
	b.halfOpenSuccesses = 0
	b.halfOpenInFlight = 0
}

// close transitions to CLOSED. Callers must hold b.mu.
func (b *Breaker) close() {
	b.state = StateClosed
	b.consecutiveFailures = 0
	b.halfOpenSuccesses = 0
	b.halfOpenInFlight = 0
}
