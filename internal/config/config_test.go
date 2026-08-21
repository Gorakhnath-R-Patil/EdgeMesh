package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/config"
)

func TestLoadAppliesDefaultsWithoutFile(t *testing.T) {
	cfg, err := config.Load("", config.Config{
		Server: config.ServerConfig{ListenAddress: "127.0.0.1:9000"},
	})
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Logging.Level != config.LevelInfo {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, config.LevelInfo)
	}
	if cfg.Logging.Format != config.FormatText {
		t.Errorf("Logging.Format = %q, want %q", cfg.Logging.Format, config.FormatText)
	}
	if cfg.Node.ID == "" {
		t.Errorf("Node.ID = %q, want a non-empty default", cfg.Node.ID)
	}
	if cfg.Server.ListenAddress != "127.0.0.1:9000" {
		t.Errorf("Server.ListenAddress = %q, want caller default preserved", cfg.Server.ListenAddress)
	}
}

func TestLoadFileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.yaml")
	yaml := `
node:
  id: proxy-1
  zone: us-east-1a
logging:
  level: debug
  format: json
server:
  listenAddress: 0.0.0.0:8080
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.Load(path, config.Config{
		Server: config.ServerConfig{ListenAddress: "127.0.0.1:9000"},
	})
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Node.ID != "proxy-1" {
		t.Errorf("Node.ID = %q, want %q", cfg.Node.ID, "proxy-1")
	}
	if cfg.Node.Zone != "us-east-1a" {
		t.Errorf("Node.Zone = %q, want %q", cfg.Node.Zone, "us-east-1a")
	}
	if cfg.Logging.Level != config.LevelDebug {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, config.LevelDebug)
	}
	if cfg.Server.ListenAddress != "0.0.0.0:8080" {
		t.Errorf("Server.ListenAddress = %q, want file value to win over default", cfg.Server.ListenAddress)
	}
}

func TestLoadEnvOverridesFileAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.yaml")
	if err := os.WriteFile(path, []byte("logging:\n  level: debug\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("EDGEMESH_LOG_LEVEL", "warn")
	t.Setenv("EDGEMESH_NODE_ID", "env-node")

	cfg, err := config.Load(path, config.Config{})
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Logging.Level != config.LevelWarn {
		t.Errorf("Logging.Level = %q, want env override %q", cfg.Logging.Level, config.LevelWarn)
	}
	if cfg.Node.ID != "env-node" {
		t.Errorf("Node.ID = %q, want env override %q", cfg.Node.ID, "env-node")
	}
}

func TestLoadMissingFileReturnsError(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"), config.Config{})
	if err == nil {
		t.Fatal("Load() error = nil, want non-nil for missing file")
	}
}

func TestLoadInvalidYAMLReturnsInvalidConfigError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("node: [this is not a mapping"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := config.Load(path, config.Config{})
	if !edgeerrors.Is(err, edgeerrors.ErrInvalidConfig) {
		t.Fatalf("Load() error = %v, want it to wrap ErrInvalidConfig", err)
	}
}

func TestValidateRejectsUnknownLogLevel(t *testing.T) {
	cfg := config.Config{
		Node:    config.NodeConfig{ID: "n1"},
		Logging: config.LoggingConfig{Level: "verbose", Format: config.FormatText},
	}
	err := cfg.Validate()
	if !edgeerrors.Is(err, edgeerrors.ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want it to wrap ErrInvalidConfig", err)
	}
}

func TestValidateRejectsUnknownLogFormat(t *testing.T) {
	cfg := config.Config{
		Node:    config.NodeConfig{ID: "n1"},
		Logging: config.LoggingConfig{Level: config.LevelInfo, Format: "xml"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error for unknown format")
	}
}

func TestValidateRejectsMalformedListenAddress(t *testing.T) {
	cfg := config.Config{
		Node:    config.NodeConfig{ID: "n1"},
		Logging: config.LoggingConfig{Level: config.LevelInfo, Format: config.FormatText},
		Server:  config.ServerConfig{ListenAddress: "not-a-host-port"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error for malformed listen address")
	}
}

func TestValidateAcceptsWellFormedConfig(t *testing.T) {
	cfg := config.Config{
		Node:    config.NodeConfig{ID: "n1"},
		Logging: config.LoggingConfig{Level: config.LevelInfo, Format: config.FormatText},
		Server:  config.ServerConfig{ListenAddress: "0.0.0.0:8080"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func baseValidConfig() config.Config {
	return config.Config{
		Node:    config.NodeConfig{ID: "n1"},
		Logging: config.LoggingConfig{Level: config.LevelInfo, Format: config.FormatText},
	}
}

func TestValidateAcceptsEmptyUpstream(t *testing.T) {
	// edgemesh-controller never sets Upstream; the schema must not
	// penalize binaries that don't use it.
	if err := baseValidConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil for empty upstream", err)
	}
}

func TestValidateAcceptsWellFormedUpstream(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Upstream = config.UpstreamConfig{Address: "http://127.0.0.1:9000"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestValidateRejectsUpstreamAddressWithoutScheme(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Upstream = config.UpstreamConfig{Address: "127.0.0.1:9000"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error for schemeless upstream address")
	}
}

func TestValidateRejectsUpstreamAddressWithBadScheme(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Upstream = config.UpstreamConfig{Address: "ftp://127.0.0.1:9000"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error for non-http(s) scheme")
	}
}

func TestValidateRejectsUpstreamAddressWithoutHost(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Upstream = config.UpstreamConfig{Address: "http:///path"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error for hostless upstream address")
	}
}

func TestValidateRejectsNegativeUpstreamTimeout(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Upstream = config.UpstreamConfig{
		Address:     "http://127.0.0.1:9000",
		DialTimeout: config.Duration{Duration: -1 * time.Second},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error for negative dial timeout")
	}
}

func TestLoadAppliesUpstreamDefaults(t *testing.T) {
	cfg, err := config.Load("", config.Config{
		Upstream: config.UpstreamConfig{Address: "http://127.0.0.1:9000"},
	})
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Upstream.DialTimeout.Duration != 5*time.Second {
		t.Errorf("Upstream.DialTimeout = %v, want 5s", cfg.Upstream.DialTimeout.Duration)
	}
	if cfg.Upstream.ResponseHeaderTimeout.Duration != 10*time.Second {
		t.Errorf("Upstream.ResponseHeaderTimeout = %v, want 10s", cfg.Upstream.ResponseHeaderTimeout.Duration)
	}
	if cfg.Upstream.RequestTimeout.Duration != 15*time.Second {
		t.Errorf("Upstream.RequestTimeout = %v, want 15s", cfg.Upstream.RequestTimeout.Duration)
	}
	if cfg.Upstream.IdleConnTimeout.Duration != 90*time.Second {
		t.Errorf("Upstream.IdleConnTimeout = %v, want 90s", cfg.Upstream.IdleConnTimeout.Duration)
	}
	if cfg.Upstream.MaxIdleConns != 100 {
		t.Errorf("Upstream.MaxIdleConns = %d, want 100", cfg.Upstream.MaxIdleConns)
	}
	if cfg.Upstream.MaxIdleConnsPerHost != 10 {
		t.Errorf("Upstream.MaxIdleConnsPerHost = %d, want 10", cfg.Upstream.MaxIdleConnsPerHost)
	}
}

func TestValidateRejectsNegativeResponseHeaderTimeout(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Upstream = config.UpstreamConfig{
		Address:               "http://127.0.0.1:9000",
		ResponseHeaderTimeout: config.Duration{Duration: -1 * time.Second},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error for negative response header timeout")
	}
}

func TestLoadUpstreamAddressEnvOverride(t *testing.T) {
	t.Setenv("EDGEMESH_UPSTREAM_ADDRESS", "http://10.0.0.9:8081")

	cfg, err := config.Load("", config.Config{})
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Upstream.Address != "http://10.0.0.9:8081" {
		t.Errorf("Upstream.Address = %q, want env override", cfg.Upstream.Address)
	}
}

func TestLoadUpstreamFileOverridesDefaultTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.yaml")
	yaml := `
upstream:
  address: http://127.0.0.1:9000
  dialTimeout: 2s
  responseHeaderTimeout: 8s
  requestTimeout: 30s
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.Load(path, config.Config{})
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Upstream.DialTimeout.Duration != 2*time.Second {
		t.Errorf("Upstream.DialTimeout = %v, want 2s", cfg.Upstream.DialTimeout.Duration)
	}
	if cfg.Upstream.ResponseHeaderTimeout.Duration != 8*time.Second {
		t.Errorf("Upstream.ResponseHeaderTimeout = %v, want 8s", cfg.Upstream.ResponseHeaderTimeout.Duration)
	}
	if cfg.Upstream.RequestTimeout.Duration != 30*time.Second {
		t.Errorf("Upstream.RequestTimeout = %v, want 30s", cfg.Upstream.RequestTimeout.Duration)
	}
	// IdleConnTimeout wasn't set in the file, so it still falls back to
	// the built-in default.
	if cfg.Upstream.IdleConnTimeout.Duration != 90*time.Second {
		t.Errorf("Upstream.IdleConnTimeout = %v, want 90s default", cfg.Upstream.IdleConnTimeout.Duration)
	}
}
