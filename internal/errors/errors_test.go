package errors_test

import (
	"fmt"
	"testing"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"
)

func TestWrapNilIsNil(t *testing.T) {
	if got := edgeerrors.Wrap("op", nil); got != nil {
		t.Fatalf("Wrap(op, nil) = %v, want nil", got)
	}
}

func TestWrapPreservesSentinelForIs(t *testing.T) {
	err := edgeerrors.Wrap("config.Load", edgeerrors.ErrInvalidConfig)

	if !edgeerrors.Is(err, edgeerrors.ErrInvalidConfig) {
		t.Fatalf("Is(err, ErrInvalidConfig) = false, want true")
	}
}

func TestWrapMessageIncludesOpAndCause(t *testing.T) {
	cause := edgeerrors.New("missing field: node.id")
	err := edgeerrors.Wrap("config.Validate", cause)

	want := "config.Validate: missing field: node.id"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestWrapUnwrapsToCause(t *testing.T) {
	cause := edgeerrors.New("boom")
	err := edgeerrors.Wrap("op", cause)

	if got := edgeerrors.Unwrap(err); got != cause {
		t.Fatalf("Unwrap(err) = %v, want %v", got, cause)
	}
}

func TestWrapChainWithFmtErrorf(t *testing.T) {
	// Sentinels must remain matchable even after further wrapping with
	// fmt.Errorf("%w", ...), since that is the idiomatic way callers will
	// add context up the stack.
	err := fmt.Errorf("registry lookup failed: %w", edgeerrors.Wrap("registry.Get", edgeerrors.ErrNotFound))

	if !edgeerrors.Is(err, edgeerrors.ErrNotFound) {
		t.Fatalf("Is(err, ErrNotFound) = false, want true")
	}
}
