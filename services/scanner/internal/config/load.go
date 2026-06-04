// Package config loads scanner configuration from YAML with ${VAR:-default}
// env substitution and applied defaults. Precedence (low → high):
// defaults in code → YAML file → env vars (via ${VAR:-default}) → CLI flags
// (the last applied by main, not here).
package config

import (
	"fmt"
	"runtime"

	kitcfg "github.com/Harporis/harporis/kit/config"
	"github.com/Harporis/harporis/kit/nats/wire"
)

// Config holds runtime config for the scanner.
type Config struct {
	NATSURL               string `yaml:"nats_url"`
	NATSToken             string `yaml:"nats_token"`
	Workers               int    `yaml:"workers"`
	FetchBatch            int    `yaml:"fetch_batch"`
	FetchMaxWaitMs        int    `yaml:"fetch_max_wait_ms"`
	AckWaitSeconds        int    `yaml:"ack_wait_seconds"`
	MaxDeliver            int    `yaml:"max_deliver"`
	MaxAckPending         int    `yaml:"max_ack_pending"`
	StatusTickMs          int    `yaml:"status_tick_ms"`
	PublishAckWaitSeconds int    `yaml:"publish_ack_wait_seconds"`
	MetricsAddr           string `yaml:"metrics_addr"`
	LogLevel              string `yaml:"log_level"`
	RulesPath             string `yaml:"rules_path"`
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
	if c.StatusTickMs <= 0 {
		c.StatusTickMs = 5000
	}
	if c.PublishAckWaitSeconds <= 0 {
		c.PublishAckWaitSeconds = 5
	}
	if c.MetricsAddr == "" {
		c.MetricsAddr = fmt.Sprintf(":%d", wire.MetricsPorts[wire.ServiceScanner])
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}
