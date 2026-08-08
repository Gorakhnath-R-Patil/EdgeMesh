package logging_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/config"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/logging"
)

func TestNewWithWriterJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewWithWriter(&buf, config.LoggingConfig{Level: config.LevelInfo, Format: config.FormatJSON}, "edgemesh-proxy")

	logger.Info("starting", "port", 8080)

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if record["component"] != "edgemesh-proxy" {
		t.Errorf("component = %v, want %q", record["component"], "edgemesh-proxy")
	}
	if record["msg"] != "starting" {
		t.Errorf("msg = %v, want %q", record["msg"], "starting")
	}
}

func TestNewWithWriterTextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewWithWriter(&buf, config.LoggingConfig{Level: config.LevelInfo, Format: config.FormatText}, "edgemesh-cli")

	logger.Info("ready")

	out := buf.String()
	if !strings.Contains(out, "component=edgemesh-cli") {
		t.Errorf("output %q does not contain component attribute", out)
	}
	if !strings.Contains(out, "msg=ready") {
		t.Errorf("output %q does not contain msg", out)
	}
}

func TestNewWithWriterRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewWithWriter(&buf, config.LoggingConfig{Level: config.LevelWarn, Format: config.FormatText}, "test")

	logger.Info("should be suppressed")
	logger.Warn("should appear")

	out := buf.String()
	if strings.Contains(out, "should be suppressed") {
		t.Errorf("Info record was emitted despite Warn level: %q", out)
	}
	if !strings.Contains(out, "should appear") {
		t.Errorf("Warn record was suppressed: %q", out)
	}
}

func TestNewWithWriterDefaultsUnknownLevelToInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewWithWriter(&buf, config.LoggingConfig{Level: "nonsense", Format: config.FormatText}, "test")

	logger.Info("visible at default level")

	if !strings.Contains(buf.String(), "visible at default level") {
		t.Errorf("Info record suppressed when it should default to info level: %q", buf.String())
	}
}
