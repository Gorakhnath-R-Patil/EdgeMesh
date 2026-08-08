// Package errors defines EdgeMesh's shared error-handling conventions:
// a small set of sentinel errors that callers can match with errors.Is,
// and an Op wrapper that records which component/operation an error
// originated from without discarding the underlying cause.
//
// Package-specific error values should be defined next to the code that
// returns them and wrapped with Wrap so they remain matchable via
// errors.Is/errors.As while carrying enough context for operators to
// diagnose failures from logs alone.
package errors

import (
	"errors"
	"fmt"
)

// Sentinel errors shared across EdgeMesh components. Concrete errors
// returned by a package should wrap one of these (via Wrap or fmt.Errorf
// with %w) so callers can branch on failure class instead of string
// matching.
var (
	// ErrInvalidConfig indicates configuration failed validation.
	ErrInvalidConfig = errors.New("invalid configuration")
	// ErrNotFound indicates a requested resource does not exist.
	ErrNotFound = errors.New("not found")
	// ErrAlreadyExists indicates a create/register operation collided
	// with an existing resource.
	ErrAlreadyExists = errors.New("already exists")
	// ErrUnavailable indicates a dependency could not be reached or is
	// not currently able to serve requests.
	ErrUnavailable = errors.New("unavailable")
)

// Is, As, Unwrap and New are re-exported so callers only need to import
// this package for ordinary error handling in addition to EdgeMesh's own
// helpers.
var (
	Is     = errors.Is
	As     = errors.As
	Unwrap = errors.Unwrap
	New    = errors.New
)

// opError associates an error with the operation that produced it, e.g.
// "config.Load" or "registry.Register". It preserves the wrapped error
// for errors.Is/errors.As while giving log output a consistent, greppable
// shape: "<op>: <cause>".
type opError struct {
	op  string
	err error
}

func (e *opError) Error() string {
	return fmt.Sprintf("%s: %v", e.op, e.err)
}

func (e *opError) Unwrap() error {
	return e.err
}

// Wrap annotates err with the operation that produced it. It returns nil
// if err is nil, so it is safe to use unconditionally:
//
//	if err := doThing(); err != nil {
//	    return errors.Wrap("proxy.Start", err)
//	}
func Wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return &opError{op: op, err: err}
}
