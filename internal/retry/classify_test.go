package retry_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/retry"
)

func TestIsIdempotentTrueForSafeMethods(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodTrace} {
		if !retry.IsIdempotent(m) {
			t.Errorf("IsIdempotent(%q) = false, want true", m)
		}
	}
}

func TestIsIdempotentFalseForUnsafeMethods(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPatch, http.MethodConnect} {
		if retry.IsIdempotent(m) {
			t.Errorf("IsIdempotent(%q) = true, want false", m)
		}
	}
}

func TestIsIdempotentCaseInsensitive(t *testing.T) {
	if !retry.IsIdempotent("get") {
		t.Error(`IsIdempotent("get") = false, want true`)
	}
	if retry.IsIdempotent("post") {
		t.Error(`IsIdempotent("post") = true, want false`)
	}
}

func TestIsRetryableErrorNilIsFalse(t *testing.T) {
	if retry.IsRetryableError(nil) {
		t.Error("IsRetryableError(nil) = true, want false")
	}
}

func TestIsRetryableErrorTransportFailureIsTrue(t *testing.T) {
	if !retry.IsRetryableError(fmt.Errorf("dial tcp: connection refused")) {
		t.Error("IsRetryableError(connection refused) = false, want true")
	}
}

func TestIsRetryableErrorDeadlineExceededIsTrue(t *testing.T) {
	if !retry.IsRetryableError(context.DeadlineExceeded) {
		t.Error("IsRetryableError(context.DeadlineExceeded) = false, want true")
	}
}

func TestIsRetryableErrorContextCanceledIsFalse(t *testing.T) {
	if retry.IsRetryableError(context.Canceled) {
		t.Error("IsRetryableError(context.Canceled) = true, want false (client gave up)")
	}
}

func TestIsRetryableErrorWrappedContextCanceledIsFalse(t *testing.T) {
	wrapped := fmt.Errorf("proxy: request failed: %w", context.Canceled)
	if retry.IsRetryableError(wrapped) {
		t.Error("IsRetryableError(wrapped context.Canceled) = true, want false")
	}
}

func TestIsRetryableErrorTrueForArbitraryError(t *testing.T) {
	if !retry.IsRetryableError(errors.New("boom")) {
		t.Fatal(`IsRetryableError(errors.New("boom")) = false, want true`)
	}
}
