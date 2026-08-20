// Package retry implements EdgeMesh's retry decision-making: whether a
// failed attempt should be retried, and how long to wait before trying
// again. It deliberately stops at the decision — driving an actual
// retry loop against a real HTTP client and a per-attempt endpoint
// selection is a proxy-integration concern for a later development
// phase, once load balancing is wired into the request path.
//
// Retries are carefully bounded, per the project's engineering
// standard: a maximum attempt count, exponential backoff with full
// jitter, and idempotency gating all exist specifically to prevent a
// struggling backend from being hit with a retry storm on top of
// whatever is already causing it to fail.
package retry

import (
	"math/rand/v2"
	"time"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// Built-in defaults, applied to any field left zero-valued (or, for
// RetryableStatusCodes, empty) in the RetryPolicy passed to
// PolicyFromProto. A nil policy gets every default.
const (
	defaultMaxAttempts   = 3
	defaultPerTryTimeout = 5 * time.Second
	defaultBackoffBase   = 50 * time.Millisecond
	defaultBackoffMax    = 2 * time.Second
)

// defaultRetryableStatusCodes is used when a policy specifies no
// explicit list: the classic "safe to retry, usually transient"
// gateway-facing set (matching what edgemesh-proxy itself already
// produces for a downed or timed-out backend — see internal/proxy).
var defaultRetryableStatusCodes = []uint32{502, 503, 504}

// Policy is the resolved, defaulted retry configuration ShouldRetry and
// Backoff act on. It's a plain Go type rather than the wire-format
// *meshv1alpha1.RetryPolicy so that after PolicyFromProto, every field
// has an unambiguous, already-defaulted value — no further nil/zero
// checks needed at the call site.
type Policy struct {
	MaxAttempts          uint32
	PerTryTimeout        time.Duration
	BackoffBase          time.Duration
	BackoffMax           time.Duration
	RetryableStatusCodes map[uint32]bool
	// RetryNonIdempotent is the explicit opt-in required to retry a
	// request whose method isn't idempotent (see IsIdempotent).
	RetryNonIdempotent bool
}

// PolicyFromProto resolves a Policy from p, applying built-in defaults
// to a nil p, or to any zero-valued/empty field within one.
func PolicyFromProto(p *meshv1alpha1.RetryPolicy) Policy {
	resolved := Policy{
		MaxAttempts:   defaultMaxAttempts,
		PerTryTimeout: defaultPerTryTimeout,
		BackoffBase:   defaultBackoffBase,
		BackoffMax:    defaultBackoffMax,
	}
	codes := defaultRetryableStatusCodes

	if p != nil {
		if p.GetMaxAttempts() > 0 {
			resolved.MaxAttempts = p.GetMaxAttempts()
		}
		if d := p.GetPerTryTimeout().AsDuration(); d > 0 {
			resolved.PerTryTimeout = d
		}
		if d := p.GetBackoffBase().AsDuration(); d > 0 {
			resolved.BackoffBase = d
		}
		if d := p.GetBackoffMax().AsDuration(); d > 0 {
			resolved.BackoffMax = d
		}
		if len(p.GetRetryableStatusCodes()) > 0 {
			codes = p.GetRetryableStatusCodes()
		}
		resolved.RetryNonIdempotent = p.GetRetryNonIdempotent()
	}

	resolved.RetryableStatusCodes = make(map[uint32]bool, len(codes))
	for _, c := range codes {
		resolved.RetryableStatusCodes[c] = true
	}
	return resolved
}

// ShouldRetry reports whether attempt — the 1-based attempt number that
// just failed — should be followed by another try of an HTTP request
// with the given method, given the outcome of that attempt: statusCode
// (0 if the request never received a response) and err (nil if it
// did). Exactly one of statusCode/err is meaningful for a given
// outcome, matching how internal/health's Classify* functions and
// internal/proxy already report results.
//
// Retrying is refused if any of these hold: attempt has already
// reached MaxAttempts; method isn't idempotent and the policy doesn't
// set RetryNonIdempotent; or neither the status code nor the error is
// classified retryable.
func (p Policy) ShouldRetry(method string, attempt uint32, statusCode int, err error) bool {
	if attempt >= p.MaxAttempts {
		return false
	}
	if !IsIdempotent(method) && !p.RetryNonIdempotent {
		return false
	}
	if statusCode != 0 {
		return p.RetryableStatusCodes[uint32(statusCode)]
	}
	return IsRetryableError(err)
}

// Backoff returns how long to wait before the next attempt after
// attempt (1-based), computed as full-jitter exponential backoff: a
// uniformly random duration in [0, min(BackoffMax, BackoffBase *
// 2^(attempt-1))]. Full jitter is the AWS-documented technique for
// preventing a retry storm: if every client backed off by the exact
// same computed delay, they'd all retry at the same instant and
// recreate the very load spike that caused the failures — spreading
// retries uniformly across the whole window avoids that. Doubling is
// computed iteratively rather than via a shift, so a large attempt
// count or BackoffBase saturates at BackoffMax instead of overflowing.
func (p Policy) Backoff(attempt uint32) time.Duration {
	if attempt == 0 {
		attempt = 1
	}

	backoff := p.BackoffBase
	for i := uint32(1); i < attempt && backoff < p.BackoffMax; i++ {
		backoff *= 2
		if backoff <= 0 { // overflowed time.Duration's int64 range
			backoff = p.BackoffMax
			break
		}
	}
	if backoff > p.BackoffMax {
		backoff = p.BackoffMax
	}
	if backoff <= 0 {
		return 0
	}

	return time.Duration(rand.Int64N(int64(backoff) + 1))
}
