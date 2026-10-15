// Package config loads, validates, and defaults kestrel.yaml.
//
// Kestrel works without any config file; a config only makes it better.
package config

import (
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// FileNames are the accepted config file names, in lookup order.
var FileNames = []string{"kestrel.yaml", ".kestrel.yaml"}

// CurrentVersion is the only supported config schema version.
const CurrentVersion = 1

// Defaults applied when a field is omitted.
const (
	DefaultRetentionDays      = 7
	DefaultMaxTotalMB         = 500
	DefaultCorrelationWindow  = 10 * time.Second
	DefaultHealthInterval     = 2 * time.Second
	DefaultHealthTimeout      = 1 * time.Second
	DefaultHealthStartupGrace = 10 * time.Second
	DefaultHealthRetries      = 3
	DefaultHTTPExpectStatus   = "200-399"
	DefaultCommandTimeout     = 120 * time.Second
)

// Config is the typed form of kestrel.yaml.
type Config struct {
	Version     int         `yaml:"version" json:"version"`
	Project     Project     `yaml:"project" json:"project"`
	Storage     Storage     `yaml:"storage" json:"storage"`
	Correlation Correlation `yaml:"correlation" json:"correlation"`
	Redaction   Redaction   `yaml:"redaction" json:"redaction"`
	Services    []Service   `yaml:"services" json:"services"`
	Verify      Verify      `yaml:"verify" json:"verify"`
}

// Project holds project metadata.
type Project struct {
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// Storage controls retention of recorded sessions.
type Storage struct {
	RetentionDays int `yaml:"retention_days" json:"retention_days"`
	MaxTotalMB    int `yaml:"max_total_mb" json:"max_total_mb"`
}

// Correlation tunes how events are grouped into incidents.
type Correlation struct {
	Window Duration `yaml:"window" json:"window"`
}

// Redaction extends the built-in secret redaction patterns.
type Redaction struct {
	ExtraPatterns []string `yaml:"extra_patterns,omitempty" json:"extra_patterns"`
}

// Service is one process Kestrel may supervise, or an external service it only probes.
type Service struct {
	Name      string            `yaml:"name" json:"name"`
	Dir       string            `yaml:"dir,omitempty" json:"dir,omitempty"`
	Command   string            `yaml:"command,omitempty" json:"command,omitempty"`
	Env       map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	DependsOn []string          `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Health    *Health           `yaml:"health,omitempty" json:"health,omitempty"`
}

// Health types.
const (
	HealthHTTP    = "http"
	HealthTCP     = "tcp"
	HealthProcess = "process"
)

// Health describes a readiness probe.
type Health struct {
	Type         string   `yaml:"type" json:"type"`
	URL          string   `yaml:"url,omitempty" json:"url,omitempty"`
	Address      string   `yaml:"address,omitempty" json:"address,omitempty"`
	ExpectStatus string   `yaml:"expect_status,omitempty" json:"expect_status,omitempty"`
	BodyContains string   `yaml:"body_contains,omitempty" json:"body_contains,omitempty"`
	Interval     Duration `yaml:"interval" json:"interval"`
	Timeout      Duration `yaml:"timeout" json:"timeout"`
	Retries      int      `yaml:"retries" json:"retries"`
	StartupGrace Duration `yaml:"startup_grace" json:"startup_grace"`
}

// Verify configures `kestrel verify`.
type Verify struct {
	Checks []Check `yaml:"checks,omitempty" json:"checks,omitempty"`
}

// Check types.
const (
	CheckProbe   = "probe"
	CheckCommand = "command"
)

// Check is one verification step.
type Check struct {
	Type    string   `yaml:"type" json:"type"`
	Service string   `yaml:"service,omitempty" json:"service,omitempty"`
	Run     string   `yaml:"run,omitempty" json:"run,omitempty"`
	Dir     string   `yaml:"dir,omitempty" json:"dir,omitempty"`
	Timeout Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// Duration is a time.Duration written as a Go duration string ("2s", "1m30s").
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML accepts Go duration strings.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: expected a duration such as \"2s\"", n.Line)
	}
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q (use values like \"500ms\", \"2s\", \"1m\")", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML writes the duration as a string.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

// MarshalJSON writes the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// ApplyDefaults fills omitted fields with their defaults.
func (c *Config) ApplyDefaults() {
	if c.Storage.RetentionDays == 0 {
		c.Storage.RetentionDays = DefaultRetentionDays
	}
	if c.Storage.MaxTotalMB == 0 {
		c.Storage.MaxTotalMB = DefaultMaxTotalMB
	}
	if c.Correlation.Window == 0 {
		c.Correlation.Window = Duration(DefaultCorrelationWindow)
	}
	for i := range c.Services {
		h := c.Services[i].Health
		if h == nil {
			continue
		}
		if h.Interval == 0 {
			h.Interval = Duration(DefaultHealthInterval)
		}
		if h.Timeout == 0 {
			h.Timeout = Duration(DefaultHealthTimeout)
		}
		if h.StartupGrace == 0 {
			h.StartupGrace = Duration(DefaultHealthStartupGrace)
		}
		if h.Retries == 0 {
			h.Retries = DefaultHealthRetries
		}
		if h.Type == HealthHTTP && h.ExpectStatus == "" {
			h.ExpectStatus = DefaultHTTPExpectStatus
		}
	}
	for i := range c.Verify.Checks {
		ch := &c.Verify.Checks[i]
		if ch.Type == CheckCommand && ch.Timeout == 0 {
			ch.Timeout = Duration(DefaultCommandTimeout)
		}
	}
}

// Service returns the named service, or nil.
func (c *Config) Service(name string) *Service {
	for i := range c.Services {
		if c.Services[i].Name == name {
			return &c.Services[i]
		}
	}
	return nil
}
