package retry

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// idempotentMethods are the HTTP methods a request can be retried with
// by default. Per RFC 9110, an idempotent method's intended effect on
// the server is the same whether it's executed once or several times.
// POST, PATCH, and CONNECT are deliberately excluded: retrying one can
// duplicate a real side effect (e.g. charging a payment twice).
var idempotentMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodPut:     true,
	http.MethodDelete:  true,
	http.MethodOptions: true,
	http.MethodTrace:   true,
}

// IsIdempotent reports whether method is safe to retry without an
// explicit opt-in (Policy.RetryNonIdempotent). Comparison is
// case-insensitive since http.Request.Method is conventionally
// uppercase but callers shouldn't have to rely on that.
func IsIdempotent(method string) bool {
	return idempotentMethods[strings.ToUpper(method)]
}

// IsRetryableError reports whether a transport-level error observed
// while attempting a request should be treated as retryable. err is
// retryable if it's non-nil and isn't (or doesn't wrap)
// context.Canceled: a connection failure, a dial error, or the
// request's own deadline expiring all mean the request never got a
// response, so trying again has a real chance of succeeding.
// context.Canceled is excluded because it means the client gave up —
// retrying would just waste work nobody is waiting for anymore.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled)
}
