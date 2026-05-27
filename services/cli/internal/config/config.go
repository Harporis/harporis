// Package config loads the optional ~/.config/harporis/config.yaml file.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk shape.
type Config struct {
	NATSURL         string `yaml:"nats_url"`
	Color           string `yaml:"color"`             // auto|always|never
	DefaultScanType string `yaml:"default_scan_type"` // current_state|...
}

// Defaults applied when nothing is set.
const (
	defaultNATSURL  = "nats://localhost:4222"
	defaultColor    = "auto"
	defaultScanType = "current_state"
)

// Load reads the config file. Missing file is not an error — defaults
// are returned. Returns the merged config.
func Load(path string) (Config, error) {
	c := Config{NATSURL: defaultNATSURL, Color: defaultColor, DefaultScanType: defaultScanType}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if c.NATSURL == "" {
		c.NATSURL = defaultNATSURL
	}
	if c.Color == "" {
		c.Color = defaultColor
	}
	if c.DefaultScanType == "" {
		c.DefaultScanType = defaultScanType
	}
	return c, nil
}

// DefaultPath returns ~/.config/harporis/config.yaml or "" if HOME unset.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "harporis", "config.yaml")
}
