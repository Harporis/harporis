// Package config loads writer configuration from YAML with ${VAR:-default}
// env substitution and applied defaults. Precedence (low → high):
// defaults in code → YAML file → env vars (via ${VAR:-default}) → CLI flags.
package config

import (
	"fmt"
	"os"
	"regexp"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config holds runtime config for the writer.
type Config struct {
	NATSURL        string `yaml:"nats_url"`
	Workers        int    `yaml:"workers"`
	FetchBatch     int    `yaml:"fetch_batch"`
	FetchMaxWaitMs int    `yaml:"fetch_max_wait_ms"`
	AckWaitSeconds int    `yaml:"ack_wait_seconds"`
	MaxDeliver     int    `yaml:"max_deliver"`
	MaxAckPending  int    `yaml:"max_ack_pending"`
	OutputDir      string `yaml:"output_dir"`
	MetricsAddr    string `yaml:"metrics_addr"`
	LogLevel       string `yaml:"log_level"`
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

func expandEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(match string) string {
		g := envPattern.FindStringSubmatch(match)
		if v, ok := os.LookupEnv(g[1]); ok && v != "" {
			return v
		}
		return g[2]
	})
}

// Load reads YAML config from path, performs ${VAR:-default} env substitution,
// and applies defaults for any unset fields.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte(expandEnv(string(raw))), &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
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
	if c.MetricsAddr == "" {
		c.MetricsAddr = ":9102"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}
