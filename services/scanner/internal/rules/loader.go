package rules

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultPack []byte

type yamlRule struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	Severity    string   `yaml:"severity"`
	Regex       string   `yaml:"regex"`
	SecretGroup int      `yaml:"secret_group"`
	Tags        []string `yaml:"tags"`
	Entropy     *struct {
		Min         float64 `yaml:"min"`
		TargetGroup int     `yaml:"target_group"`
	} `yaml:"entropy"`
	Examples struct {
		Positive []string `yaml:"positive"`
		Negative []string `yaml:"negative"`
	} `yaml:"examples"`
}

// LoadEmbedded returns the rule pack baked into the binary at build time.
func LoadEmbedded() ([]Rule, error) {
	return parse(defaultPack, "<embedded>")
}

// LoadFile reads a YAML rule pack from path. Used by the --rules CLI flag.
func LoadFile(path string) ([]Rule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rule pack %s: %w", path, err)
	}
	return parse(b, path)
}

func parse(b []byte, src string) ([]Rule, error) {
	var raw []yamlRule
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse rule pack %s: %w", src, err)
	}
	out := make([]Rule, 0, len(raw))
	for i, y := range raw {
		sev, err := SeverityFromString(y.Severity)
		if err != nil {
			return nil, fmt.Errorf("%s rule[%d] (%s): %w", src, i, y.ID, err)
		}
		re, err := regexp.Compile(y.Regex)
		if err != nil {
			return nil, fmt.Errorf("%s rule[%d] (%s): compile regex: %w", src, i, y.ID, err)
		}
		r := Rule{
			ID:          y.ID,
			Description: y.Description,
			Severity:    sev,
			Regex:       re,
			Tags:        y.Tags,
			SecretGrp:   y.SecretGroup,
			posExamples: y.Examples.Positive,
			negExamples: y.Examples.Negative,
		}
		if y.Entropy != nil {
			r.EntropyMin = y.Entropy.Min
			r.EntropyGrp = y.Entropy.TargetGroup
		}
		out = append(out, r)
	}
	return out, nil
}

// Validate checks that the rule pack is internally consistent:
//   - all IDs are unique
//   - every positive example matches the regex
//   - no negative example matches the regex
//
// Returns the first failure encountered.
func Validate(rules []Rule) error {
	seen := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		if _, dup := seen[r.ID]; dup {
			return fmt.Errorf("duplicate rule id %q", r.ID)
		}
		seen[r.ID] = struct{}{}
		for _, ex := range r.posExamples {
			if !r.Regex.MatchString(ex) {
				return fmt.Errorf("rule %s: positive example %q does not match regex %q", r.ID, ex, r.Regex.String())
			}
		}
		for _, ex := range r.negExamples {
			if r.Regex.MatchString(ex) {
				return fmt.Errorf("rule %s: negative example %q matches regex %q (should not)", r.ID, ex, r.Regex.String())
			}
		}
	}
	return nil
}
