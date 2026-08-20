package retry_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/retry"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// ---- PolicyFromProto defaults ----

func TestPolicyFromProtoNilAppliesAllDefaults(t *testing.T) {
	p := retry.PolicyFromProto(nil)

	if p.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", p.MaxAttempts)
	}
	if p.PerTryTimeout != 5*time.Second {
		t.Errorf("PerTryTimeout = %v, want 5s", p.PerTryTimeout)
	}
	if p.BackoffBase != 50*time.Millisecond {
		t.Errorf("BackoffBase = %v, want 50ms", p.BackoffBase)
	}
	if p.BackoffMax != 2*time.Second {
		t.Errorf("BackoffMax = %v, want 2s", p.BackoffMax)
	}
	for _, code := range []uint32{502, 503, 504} {
		if !p.RetryableStatusCodes[code] {
			t.Errorf("RetryableStatusCodes[%d] = false, want true (default set)", code)
		}
	}
	if p.RetryNonIdempotent {
		t.Error("RetryNonIdempotent = true, want false by default")
	}
}

func TestPolicyFromProtoAppliesFieldLevelDefaults(t *testing.T) {
	// Only MaxAttempts set; everything else must still default.
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{MaxAttempts: 7})

	if p.MaxAttempts != 7 {
		t.Errorf("MaxAttempts = %d, want 7", p.MaxAttempts)
	}
	if p.BackoffBase != 50*time.Millisecond {
		t.Errorf("BackoffBase = %v, want default 50ms", p.BackoffBase)
	}
}

func TestPolicyFromProtoCustomRetryableStatusCodesReplaceDefault(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{RetryableStatusCodes: []uint32{429}})

	if p.RetryableStatusCodes[502] {
		t.Error("RetryableStatusCodes[502] = true, want false (custom list replaces the default, not merges)")
	}
	if !p.RetryableStatusCodes[429] {
		t.Error("RetryableStatusCodes[429] = false, want true")
	}
}

func TestPolicyFromProtoHonorsExplicitDurations(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{
		PerTryTimeout: durationpb.New(1500 * time.Millisecond),
		BackoffBase:   durationpb.New(10 * time.Millisecond),
		BackoffMax:    durationpb.New(500 * time.Millisecond),
	})

	if p.PerTryTimeout != 1500*time.Millisecond {
		t.Errorf("PerTryTimeout = %v, want 1500ms", p.PerTryTimeout)
	}
	if p.BackoffBase != 10*time.Millisecond {
		t.Errorf("BackoffBase = %v, want 10ms", p.BackoffBase)
	}
	if p.BackoffMax != 500*time.Millisecond {
		t.Errorf("BackoffMax = %v, want 500ms", p.BackoffMax)
	}
}

func TestPolicyFromProtoHonorsRetryNonIdempotent(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{RetryNonIdempotent: true})
	if !p.RetryNonIdempotent {
		t.Error("RetryNonIdempotent = false, want true")
	}
}

// ---- ShouldRetry: attempt limit ----

func TestShouldRetryRefusesAtMaxAttempts(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{MaxAttempts: 3})

	if !p.ShouldRetry(http.MethodGet, 1, 503, nil) {
		t.Error("ShouldRetry(attempt=1) = false, want true (1 < 3)")
	}
	if !p.ShouldRetry(http.MethodGet, 2, 503, nil) {
		t.Error("ShouldRetry(attempt=2) = false, want true (2 < 3)")
	}
	if p.ShouldRetry(http.MethodGet, 3, 503, nil) {
		t.Error("ShouldRetry(attempt=3) = true, want false (3 >= MaxAttempts)")
	}
	if p.ShouldRetry(http.MethodGet, 4, 503, nil) {
		t.Error("ShouldRetry(attempt=4) = true, want false")
	}
}

// ---- ShouldRetry: idempotency (explicitly required test coverage) ----

func TestShouldRetryAllowsIdempotentMethodsByDefault(t *testing.T) {
	p := retry.PolicyFromProto(nil)
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodTrace} {
		if !p.ShouldRetry(m, 1, 503, nil) {
			t.Errorf("ShouldRetry(%s) = false, want true (idempotent, retryable status)", m)
		}
	}
}

func TestShouldRetryRefusesNonIdempotentMethodsByDefault(t *testing.T) {
	p := retry.PolicyFromProto(nil)
	for _, m := range []string{http.MethodPost, http.MethodPatch} {
		if p.ShouldRetry(m, 1, 503, nil) {
			t.Errorf("ShouldRetry(%s) = true, want false (non-idempotent, no explicit opt-in)", m)
		}
	}
}

func TestShouldRetryAllowsNonIdempotentWithExplicitOptIn(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{RetryNonIdempotent: true})
	if !p.ShouldRetry(http.MethodPost, 1, 503, nil) {
		t.Error("ShouldRetry(POST) = false, want true (RetryNonIdempotent explicitly set)")
	}
}

func TestShouldRetryNonIdempotentStillRespectsAttemptLimitAndClassification(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{MaxAttempts: 2, RetryNonIdempotent: true})

	if p.ShouldRetry(http.MethodPost, 2, 503, nil) {
		t.Error("ShouldRetry(POST, attempt=2) = true, want false (still bound by MaxAttempts even with opt-in)")
	}
	if p.ShouldRetry(http.MethodPost, 1, 404, nil) {
		t.Error("ShouldRetry(POST, 404) = true, want false (opt-in affects idempotency only, not status classification)")
	}
}

// ---- ShouldRetry: status code / error classification ----

func TestShouldRetryDefaultStatusCodes(t *testing.T) {
	p := retry.PolicyFromProto(nil)
	for _, code := range []int{502, 503, 504} {
		if !p.ShouldRetry(http.MethodGet, 1, code, nil) {
			t.Errorf("ShouldRetry(status=%d) = false, want true", code)
		}
	}
}

func TestShouldRetryRefusesNonRetryableStatus(t *testing.T) {
	p := retry.PolicyFromProto(nil)
	for _, code := range []int{200, 400, 404, 500} {
		if p.ShouldRetry(http.MethodGet, 1, code, nil) {
			t.Errorf("ShouldRetry(status=%d) = true, want false (not in the retryable set)", code)
		}
	}
}

func TestShouldRetryTransportError(t *testing.T) {
	p := retry.PolicyFromProto(nil)
	if !p.ShouldRetry(http.MethodGet, 1, 0, fmt.Errorf("dial tcp: connection refused")) {
		t.Error("ShouldRetry(transport error) = false, want true")
	}
}

func TestShouldRetryRefusesContextCanceled(t *testing.T) {
	p := retry.PolicyFromProto(nil)
	if p.ShouldRetry(http.MethodGet, 1, 0, context.Canceled) {
		t.Error("ShouldRetry(context.Canceled) = true, want false")
	}
}

func TestShouldRetryStatusCodeTakesPrecedenceOverError(t *testing.T) {
	// A non-zero statusCode means a response WAS received, so err (if
	// also somehow set) is not the relevant signal.
	p := retry.PolicyFromProto(nil)
	if p.ShouldRetry(http.MethodGet, 1, 404, fmt.Errorf("irrelevant")) {
		t.Error("ShouldRetry(404, err) = true, want false (status 404 is not retryable, and takes precedence)")
	}
}

// ---- Backoff ----

func TestBackoffNeverExceedsBackoffMax(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{
		BackoffBase: durationpb.New(50 * time.Millisecond),
		BackoffMax:  durationpb.New(200 * time.Millisecond),
	})

	for attempt := uint32(1); attempt <= 10; attempt++ {
		for i := 0; i < 100; i++ {
			d := p.Backoff(attempt)
			if d < 0 || d > 200*time.Millisecond {
				t.Fatalf("Backoff(%d) = %v, want in [0, 200ms]", attempt, d)
			}
		}
	}
}

func TestBackoffGrowsWithAttemptBeforeSaturating(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{
		BackoffBase: durationpb.New(10 * time.Millisecond),
		BackoffMax:  durationpb.New(10 * time.Second), // high enough that attempts 1-4 don't saturate
	})

	// Backoff is randomized (full jitter), so compare the maximum
	// observed value per attempt across many samples -- that maximum
	// converges to the attempt's (deterministic) cap, which must
	// increase attempt over attempt: 10ms, 20ms, 40ms, 80ms.
	maxObserved := func(attempt uint32) time.Duration {
		var max time.Duration
		for i := 0; i < 500; i++ {
			if d := p.Backoff(attempt); d > max {
				max = d
			}
		}
		return max
	}

	var prev time.Duration
	for attempt := uint32(1); attempt <= 4; attempt++ {
		got := maxObserved(attempt)
		// Allow a little slack below the theoretical cap since this is
		// a max-of-500-samples estimate, not the true supremum.
		if float64(got) < 0.8*float64(prev)*2 && attempt > 1 {
			t.Errorf("attempt %d: max observed backoff %v did not grow roughly 2x from attempt %d's %v", attempt, got, attempt-1, prev)
		}
		prev = got
	}
}

func TestBackoffAttemptZeroTreatedAsOne(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{
		BackoffBase: durationpb.New(50 * time.Millisecond),
		BackoffMax:  durationpb.New(time.Second),
	})
	for i := 0; i < 50; i++ {
		if d := p.Backoff(0); d > 50*time.Millisecond {
			t.Fatalf("Backoff(0) = %v, want treated like attempt 1 (<= 50ms)", d)
		}
	}
}

func TestBackoffHandlesBaseGreaterThanMaxGracefully(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{
		BackoffBase: durationpb.New(5 * time.Second),
		BackoffMax:  durationpb.New(1 * time.Second),
	})
	for attempt := uint32(1); attempt <= 5; attempt++ {
		if d := p.Backoff(attempt); d > time.Second {
			t.Fatalf("Backoff(%d) = %v, want clamped to BackoffMax (1s) even though BackoffBase exceeds it", attempt, d)
		}
	}
}

func TestBackoffDoesNotOverflowForLargeAttempts(t *testing.T) {
	p := retry.PolicyFromProto(&meshv1alpha1.RetryPolicy{
		BackoffBase: durationpb.New(time.Second),
		BackoffMax:  durationpb.New(30 * time.Second),
	})
	// A pathologically large attempt count must saturate at BackoffMax,
	// not overflow time.Duration's int64 nanosecond range or go
	// negative.
	for _, attempt := range []uint32{20, 32, 63, 64, 100, math.MaxUint32} {
		d := p.Backoff(attempt)
		if d < 0 || d > 30*time.Second {
			t.Errorf("Backoff(%d) = %v, want in [0, 30s]", attempt, d)
		}
	}
}

func TestBackoffZeroBackoffBaseReturnsZero(t *testing.T) {
	p := retry.Policy{BackoffBase: 0, BackoffMax: time.Second}
	if d := p.Backoff(1); d != 0 {
		t.Errorf("Backoff(1) = %v, want 0 when BackoffBase is 0", d)
	}
}
