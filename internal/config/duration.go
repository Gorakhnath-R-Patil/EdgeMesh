package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration so configuration fields can be written
// as human-readable YAML strings ("5s", "250ms") instead of raw
// nanosecond integers. time.Duration's underlying int64 has no such
// support on its own.
type Duration struct {
	time.Duration
}

// MarshalYAML renders the duration the same way it's written in config
// files, e.g. "5s".
func (d Duration) MarshalYAML() (interface{}, error) {
	return d.Duration.String(), nil
}

// UnmarshalYAML parses a YAML scalar as a Go duration string.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration: %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duration: invalid value %q: %w", s, err)
	}
	d.Duration = parsed
	return nil
}
