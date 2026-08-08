// Package logging provides EdgeMesh's structured logging setup on top of
// the standard library's log/slog. Every binary and package should log
// through a *slog.Logger obtained here rather than the default logger or
// fmt.Print*, so log level, format, and the "component" field stay
// consistent across the proxy, controller, and CLI.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/config"
)

// New returns a logger configured from cfg, writing to stdout and tagged
// with a "component" attribute (e.g. "edgemesh-proxy") on every record.
func New(cfg config.LoggingConfig, component string) *slog.Logger {
	return NewWithWriter(os.Stdout, cfg, component)
}

// NewWithWriter is like New but writes to an arbitrary io.Writer. It
// exists primarily so tests can assert on log output without touching
// stdout.
func NewWithWriter(w io.Writer, cfg config.LoggingConfig, component string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}

	var handler slog.Handler
	if strings.EqualFold(cfg.Format, config.FormatJSON) {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	return slog.New(handler).With("component", component)
}

// parseLevel maps a config.LoggingConfig.Level string to a slog.Level,
// defaulting to Info for unrecognized input. Config validation is
// responsible for rejecting unrecognized levels before they reach here;
// this fallback only protects against a logger being constructed from an
// unvalidated Config.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case config.LevelDebug:
		return slog.LevelDebug
	case config.LevelWarn:
		return slog.LevelWarn
	case config.LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
