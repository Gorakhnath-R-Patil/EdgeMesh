package health_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/health"
)

func TestClassifyHTTPStatusTreats5xxAsFailure(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504, 599} {
		if err := health.ClassifyHTTPStatus(status); err == nil {
			t.Errorf("ClassifyHTTPStatus(%d) = nil, want a failure error", status)
		}
	}
}

func TestClassifyHTTPStatusTreats4xxAsSuccess(t *testing.T) {
	// 4xx reflects the request, not the backend's health.
	for _, status := range []int{400, 401, 403, 404, 429} {
		if err := health.ClassifyHTTPStatus(status); err != nil {
			t.Errorf("ClassifyHTTPStatus(%d) = %v, want nil (client errors are not backend health failures)", status, err)
		}
	}
}

func TestClassifyHTTPStatusTreats2xxAndRedirectsAsSuccess(t *testing.T) {
	for _, status := range []int{200, 201, 204, 301, 304} {
		if err := health.ClassifyHTTPStatus(status); err != nil {
			t.Errorf("ClassifyHTTPStatus(%d) = %v, want nil", status, err)
		}
	}
}

func TestClassifyErrorTreatsNilAsSuccess(t *testing.T) {
	if err := health.ClassifyError(nil); err != nil {
		t.Errorf("ClassifyError(nil) = %v, want nil", err)
	}
}

func TestClassifyErrorTreatsTransportFailureAsFailure(t *testing.T) {
	transportErr := fmt.Errorf("dial tcp 10.0.0.1:8080: connection refused")
	if err := health.ClassifyError(transportErr); err == nil {
		t.Fatal("ClassifyError() = nil, want a failure for a connection error")
	}
}

func TestClassifyErrorExcludesContextCanceled(t *testing.T) {
	if err := health.ClassifyError(context.Canceled); err != nil {
		t.Errorf("ClassifyError(context.Canceled) = %v, want nil (client gave up, not a backend failure)", err)
	}
}

func TestClassifyErrorExcludesWrappedContextCanceled(t *testing.T) {
	wrapped := fmt.Errorf("proxy: request failed: %w", context.Canceled)
	if err := health.ClassifyError(wrapped); err != nil {
		t.Errorf("ClassifyError(wrapped context.Canceled) = %v, want nil", err)
	}
}

func TestClassifyErrorTreatsDeadlineExceededAsFailure(t *testing.T) {
	// Unlike Canceled, a deadline the backend itself failed to meet in
	// time is a real health signal.
	if err := health.ClassifyError(context.DeadlineExceeded); err == nil {
		t.Fatal("ClassifyError(context.DeadlineExceeded) = nil, want a failure (a timeout is a real health signal)")
	}
	if !errors.Is(health.ClassifyError(context.DeadlineExceeded), context.DeadlineExceeded) {
		t.Error("ClassifyError(context.DeadlineExceeded) does not preserve the underlying error for errors.Is")
	}
}
