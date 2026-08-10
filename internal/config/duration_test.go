package config_test

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/config"
)

func TestDurationUnmarshalYAML(t *testing.T) {
	var d config.Duration
	if err := yaml.Unmarshal([]byte(`5s`), &d); err != nil {
		t.Fatalf("Unmarshal() error = %v, want nil", err)
	}
	if d.Duration != 5*time.Second {
		t.Errorf("Duration = %v, want 5s", d.Duration)
	}
}

func TestDurationUnmarshalYAMLSubSecond(t *testing.T) {
	var d config.Duration
	if err := yaml.Unmarshal([]byte(`250ms`), &d); err != nil {
		t.Fatalf("Unmarshal() error = %v, want nil", err)
	}
	if d.Duration != 250*time.Millisecond {
		t.Errorf("Duration = %v, want 250ms", d.Duration)
	}
}

func TestDurationUnmarshalYAMLInvalid(t *testing.T) {
	var d config.Duration
	if err := yaml.Unmarshal([]byte(`not-a-duration`), &d); err == nil {
		t.Fatal("Unmarshal() error = nil, want error for invalid duration string")
	}
}

func TestDurationUnmarshalYAMLRejectsBareNumber(t *testing.T) {
	// Bare numbers are ambiguous (seconds? nanoseconds?) and time.ParseDuration
	// requires a unit suffix, so this must fail rather than silently guess.
	var d config.Duration
	if err := yaml.Unmarshal([]byte(`5`), &d); err == nil {
		t.Fatal("Unmarshal() error = nil, want error for unit-less duration")
	}
}

func TestDurationMarshalYAMLRoundTrips(t *testing.T) {
	in := config.Duration{Duration: 90 * time.Second}

	out, err := yaml.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal() error = %v, want nil", err)
	}

	var back config.Duration
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("Unmarshal(Marshal()) error = %v, want nil", err)
	}
	if back.Duration != in.Duration {
		t.Errorf("round-tripped Duration = %v, want %v", back.Duration, in.Duration)
	}
}

func TestDurationEmbeddedInStruct(t *testing.T) {
	type upstream struct {
		Timeout config.Duration `yaml:"timeout"`
	}

	var u upstream
	if err := yaml.Unmarshal([]byte("timeout: 15s\n"), &u); err != nil {
		t.Fatalf("Unmarshal() error = %v, want nil", err)
	}
	if u.Timeout.Duration != 15*time.Second {
		t.Errorf("Timeout = %v, want 15s", u.Timeout.Duration)
	}
}
