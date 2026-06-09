// Package config holds cross-service config loading helpers — env-var
// substitution and the YAML-with-substitution Load shape used by
// scanner/writer/getter. The substitution syntax is `${VAR:-default}`,
// the same minimal shell-like form already accepted by every service.
//
// Precedence (low -> high):
//   defaults in code  ->  YAML file  ->  env vars (via ${VAR:-default})
//                                    ->  CLI flags (applied by main, not here)
package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// envPattern matches ${VAR} or ${VAR:-default}.
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// ExpandEnv replaces every ${VAR:-default} occurrence in s with the value
// of os.Getenv(VAR) — falling back to default when the env var is empty
// or unset.
func ExpandEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(match string) string {
		g := envPattern.FindStringSubmatch(match)
		if v, ok := os.LookupEnv(g[1]); ok && v != "" {
			return v
		}
		return g[2]
	})
}

// LoadYAML reads path, performs ExpandEnv on the contents, and
// unmarshals into out. The caller is responsible for applying defaults
// to zero fields after the call returns.
func LoadYAML(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal([]byte(ExpandEnv(string(raw))), out); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	return nil
}
