// Package buildinfo exposes version metadata that is stamped into each
// EdgeMesh binary at build time via -ldflags (see the Makefile).
package buildinfo

import (
	"fmt"
	"runtime"
)

// These variables are overridden at build time with -ldflags "-X ...".
// The defaults below apply to unstamped `go build`/`go run` invocations
// (e.g. local development).
var (
	// Version is the EdgeMesh release version (e.g. "v0.1.0").
	Version = "dev"
	// Commit is the git commit SHA the binary was built from.
	Commit = "none"
	// Date is the UTC build timestamp in RFC3339 format.
	Date = "unknown"
)

// String renders a single-line, human-readable build summary suitable for
// startup logs and `<binary> version` output.
func String(component string) string {
	return fmt.Sprintf("%s %s (commit=%s, built=%s, go=%s)", component, Version, Commit, Date, runtime.Version())
}
