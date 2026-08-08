package config_test

import (
	"os"
	"path/filepath"
	"testing"

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
