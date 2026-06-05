// Package config loads writer configuration from YAML with ${VAR:-default}
// env substitution and applied defaults. Precedence (low → high):
// defaults in code → YAML file → env vars (via ${VAR:-default}) → CLI flags.
package config

import (
	"fmt"
	"runtime"

	kitcfg "github.com/Harporis/harporis/kit/config"
	"github.com/Harporis/harporis/kit/nats/wire"
)

// Config holds runtime config for the writer.
type Config struct {
	NATSURL        string `yaml:"nats_url"`
	NATSToken      string `yaml:"nats_token"`
	Workers        int    `yaml:"workers"`
	FetchBatch     int    `yaml:"fetch_batch"`
	FetchMaxWaitMs int    `yaml:"fetch_max_wait_ms"`
	AckWaitSeconds int    `yaml:"ack_wait_seconds"`
	MaxDeliver     int    `yaml:"max_deliver"`
	MaxAckPending  int    `yaml:"max_ack_pending"`
	OutputDir      string `yaml:"output_dir"`
	// Sink toggles. Both default to true so the operator-zero-config
	// case writes both NDJSON (streaming-friendly) and SARIF (industry-
	// standard for code-scanning tools).
	NDJSONEnabled *bool  `yaml:"ndjson_enabled"`
	SARIFEnabled  *bool  `yaml:"sarif_enabled"`
	HTMLEnabled   *bool  `yaml:"html_enabled"`
	XLSXEnabled   *bool  `yaml:"xlsx_enabled"`
	PDFEnabled    *bool  `yaml:"pdf_enabled"`
	MetricsAddr   string `yaml:"metrics_addr"`
	LogLevel      string `yaml:"log_level"`
}

// Load reads YAML config from path (via kit/config.LoadYAML which
// performs ${VAR:-default} env substitution), then applies defaults
// for any unset fields.
func Load(path string) (*Config, error) {
	var cfg Config
	if err := kitcfg.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

func applyDefaults(c *Config) {
	if c.NATSURL == "" {
		c.NATSURL = "nats://nats:4222"
	}
	if c.Workers <= 0 {
		c.Workers = runtime.NumCPU()
	}
	if c.FetchBatch <= 0 {
		c.FetchBatch = 16
	}
	if c.FetchMaxWaitMs <= 0 {
		c.FetchMaxWaitMs = 5000
	}
	if c.AckWaitSeconds <= 0 {
		c.AckWaitSeconds = 30
	}
	if c.MaxDeliver <= 0 {
		c.MaxDeliver = 5
	}
	if c.MaxAckPending <= 0 {
		c.MaxAckPending = 64
	}
	if c.OutputDir == "" {
		c.OutputDir = "/var/lib/harporis/findings"
	}
	if c.NDJSONEnabled == nil {
		v := true
		c.NDJSONEnabled = &v
	}
	if c.SARIFEnabled == nil {
		v := true
		c.SARIFEnabled = &v
	}
	if c.HTMLEnabled == nil {
		v := true
		c.HTMLEnabled = &v
	}
	if c.XLSXEnabled == nil {
		v := true
		c.XLSXEnabled = &v
	}
	if c.PDFEnabled == nil {
		v := true
		c.PDFEnabled = &v
	}
	if c.MetricsAddr == "" {
		c.MetricsAddr = fmt.Sprintf(":%d", wire.MetricsPorts[wire.ServiceWriter])
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}
