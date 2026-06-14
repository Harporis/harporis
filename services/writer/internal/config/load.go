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
	NATSCredsFile  string `yaml:"nats_creds_file"` // JWT/nkey creds for prod
	NATSRootCAs    string `yaml:"nats_root_cas"`   // PEM bundle for TLS
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
	NDJSONEnabled  *bool  `yaml:"ndjson_enabled"`
	SARIFEnabled   *bool  `yaml:"sarif_enabled"`
	HTMLEnabled    *bool  `yaml:"html_enabled"`
	XLSXEnabled    *bool  `yaml:"xlsx_enabled"`
	PDFEnabled     *bool  `yaml:"pdf_enabled"`
	ParquetEnabled *bool  `yaml:"parquet_enabled"`
	SQLiteEnabled  *bool  `yaml:"sqlite_enabled"`
	// FlushBatch — accumulator sinks (SARIF/HTML/XLSX/PDF/Parquet)
	// flush after this many NEW findings; <= 1 = sync flush on every
	// Finding (legacy O(N^2) behaviour).
	FlushBatch int `yaml:"flush_batch"`
	// FlushIntervalMs — periodic ticker that catches idle buffers so
	// partial-scan reports stay fresh; 0 disables the ticker.
	FlushIntervalMs int `yaml:"flush_interval_ms"`
	// FinalizeGraceMs — delay between observing a terminal ScanState
	// event on HARPORIS_STATUS and actually calling Finalize on the
	// sinks. Buys the pipeline (scanner → writer) time to drain after
	// getter's "scan finished" event arrives at writer.
	FinalizeGraceMs int `yaml:"finalize_grace_ms"`
	// MaskSecrets, when true, renders matched_secret as first-4 chars +
	// "***" in human-facing sinks (HTML + PDF). NDJSON/SARIF/XLSX still
	// carry the raw secret because their primary use cases (jq
	// pipelines, code-scanning ingestion, spreadsheet triage) need it.
	// Default: false (don't break existing operator expectations).
	MaskSecrets *bool `yaml:"mask_secrets"`
	// Findings retention. Both default to 0 (disabled) so existing
	// operators see no behaviour change. Set either to enable the
	// hourly sweep that prunes old sink files from output_dir.
	//   RetentionAgeHours: delete files older than N hours (0 = off)
	//   RetentionMaxBytes: oldest-first evict until total ≤ N bytes (0 = off)
	//   RetentionIntervalSeconds: sweep cadence (default 3600).
	RetentionAgeHours       int   `yaml:"retention_age_hours"`
	RetentionMaxBytes       int64 `yaml:"retention_max_bytes"`
	RetentionIntervalSeconds int  `yaml:"retention_interval_seconds"`
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
	if c.ParquetEnabled == nil {
		v := true
		c.ParquetEnabled = &v
	}
	if c.SQLiteEnabled == nil {
		// Default OFF — keeps the on-disk footprint of an idle stack to
		// the existing six sinks. Operators opt in when they need
		// cross-scan queries.
		v := false
		c.SQLiteEnabled = &v
	}
	if c.MaskSecrets == nil {
		v := false
		c.MaskSecrets = &v
	}
	if c.FlushBatch <= 0 {
		c.FlushBatch = 50
	}
	if c.FlushIntervalMs <= 0 {
		c.FlushIntervalMs = 2000
	}
	if c.FinalizeGraceMs <= 0 {
		c.FinalizeGraceMs = 10_000
	}
	if c.RetentionIntervalSeconds <= 0 {
		c.RetentionIntervalSeconds = 3600
	}
	if c.MetricsAddr == "" {
		c.MetricsAddr = fmt.Sprintf(":%d", wire.MetricsPorts[wire.ServiceWriter])
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}
