// Package config defines EdgeMesh's process-level configuration model:
// the settings every binary needs regardless of role (node identity,
// logging, and its own listen address). Domain configuration for the
// service mesh itself — services, routes, policies — is introduced in
// later development phases and will live in its own package.
//
// Configuration is loaded from an optional YAML file layered on top of
// caller-supplied defaults, then overridden by environment variables,
// then validated. A Config that fails validation is never returned to
// the caller; the previous, already-validated Config should be kept in
// that case.
package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"
	"gopkg.in/yaml.v3"
)

// Valid values for LoggingConfig.Level and LoggingConfig.Format.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"

	FormatText = "text"
	FormatJSON = "json"
)

// NodeConfig identifies the process running a given EdgeMesh binary.
// Zone/Region are carried here as plain runtime identity today; they
// become inputs to locality-aware routing once that subsystem exists.
type NodeConfig struct {
	ID     string `yaml:"id"`
	Zone   string `yaml:"zone"`
	Region string `yaml:"region"`
}

// LoggingConfig controls structured log output.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// ServerConfig controls the binary's own listener. Each binary supplies
// its own default address (e.g. the proxy and the controller listen on
// different ports).
type ServerConfig struct {
	ListenAddress string `yaml:"listenAddress"`
}

// Config is the top-level configuration shared by every EdgeMesh binary.
type Config struct {
	Node    NodeConfig    `yaml:"node"`
	Logging LoggingConfig `yaml:"logging"`
	Server  ServerConfig  `yaml:"server"`
}

// envPrefix namespaces every EdgeMesh environment variable override.
const envPrefix = "EDGEMESH_"

// Load builds a Config for one binary. defaults supplies the
// component-specific baseline (e.g. its default listen address); path,
// when non-empty, points at a YAML file whose values override that
// baseline; environment variables prefixed with EDGEMESH_ override both.
// The result is validated before being returned — Load never returns a
// Config that failed validation.
func Load(path string, defaults Config) (*Config, error) {
	const op = "config.Load"

	cfg := defaults

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, edgeerrors.Wrap(op, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, edgeerrors.Wrap(op, fmt.Errorf("%w: %v", edgeerrors.ErrInvalidConfig, err))
		}
	}

	applyEnvOverrides(&cfg)
	applyDefaults(&cfg)

	if err := cfg.Validate(); err != nil {
		return nil, edgeerrors.Wrap(op, err)
	}

	return &cfg, nil
}

// applyDefaults fills fields that are still empty after file and
// environment overrides with EdgeMesh-wide defaults. Component-specific
// defaults (like ServerConfig.ListenAddress) are the caller's
// responsibility via the defaults argument to Load.
func applyDefaults(cfg *Config) {
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = LevelInfo
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = FormatText
	}
	if cfg.Node.ID == "" {
		if host, err := os.Hostname(); err == nil && host != "" {
			cfg.Node.ID = host
		} else {
			cfg.Node.ID = "unknown-node"
		}
	}
}

// applyEnvOverrides layers environment variables on top of cfg. Only a
// small, explicit set is supported today; it grows alongside the
// configuration model rather than being derived reflectively, so every
// override is grep-able.
func applyEnvOverrides(cfg *Config) {
	if v, ok := lookupEnv("NODE_ID"); ok {
		cfg.Node.ID = v
	}
	if v, ok := lookupEnv("NODE_ZONE"); ok {
		cfg.Node.Zone = v
	}
	if v, ok := lookupEnv("NODE_REGION"); ok {
		cfg.Node.Region = v
	}
	if v, ok := lookupEnv("LOG_LEVEL"); ok {
		cfg.Logging.Level = v
	}
	if v, ok := lookupEnv("LOG_FORMAT"); ok {
		cfg.Logging.Format = v
	}
	if v, ok := lookupEnv("LISTEN_ADDRESS"); ok {
		cfg.Server.ListenAddress = v
	}
}

func lookupEnv(suffix string) (string, bool) {
	v, ok := os.LookupEnv(envPrefix + suffix)
	if !ok || strings.TrimSpace(v) == "" {
		return "", false
	}
	return v, true
}

// Validate rejects a Config that would produce undefined or unsafe
// runtime behavior. It never mutates cfg.
func (c Config) Validate() error {
	var errs []string

	switch c.Logging.Level {
	case LevelDebug, LevelInfo, LevelWarn, LevelError:
	default:
		errs = append(errs, fmt.Sprintf("logging.level: must be one of debug|info|warn|error, got %q", c.Logging.Level))
	}

	switch c.Logging.Format {
	case FormatText, FormatJSON:
	default:
		errs = append(errs, fmt.Sprintf("logging.format: must be one of text|json, got %q", c.Logging.Format))
	}

	if strings.TrimSpace(c.Node.ID) == "" {
		errs = append(errs, "node.id: must not be empty")
	}

	if c.Server.ListenAddress != "" {
		if _, _, err := net.SplitHostPort(c.Server.ListenAddress); err != nil {
			errs = append(errs, fmt.Sprintf("server.listenAddress: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%w: %s", edgeerrors.ErrInvalidConfig, strings.Join(errs, "; "))
	}
	return nil
}
