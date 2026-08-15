package health

import (
	"context"
	"errors"
	"fmt"
)

// ClassifyHTTPStatus reports whether an HTTP response status observed
// on a real, proxied request should count as a passive health failure:
// non-nil (a failure) for any 5xx status, nil (a success) otherwise.
// 4xx reflects a problem with the request, not the backend's health,
// so it is deliberately never treated as a failure signal here.
func ClassifyHTTPStatus(status int) error {
	if status >= 500 && status <= 599 {
		return fmt.Errorf("received %d response", status)
	}
	return nil
}

// ClassifyError reports whether a transport-level error observed while
// proxying a request — a connection failure, a dial timeout, a DNS
// failure, the upstream closing the connection, and so on — should
// count as a passive health failure: err itself, if non-nil, is
// unambiguously a failure signal, with one exception. context.Canceled
// (and anything wrapping it) means the client gave up on the request,
// not that the backend failed, so it is deliberately excluded — the
// backend may have been about to respond successfully.
func ClassifyError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
