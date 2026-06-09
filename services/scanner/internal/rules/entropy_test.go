package rules

import (
	"math"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestShannonEntropy(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want float64 // tolerance ±0.01
	}{
		{"empty", []byte(""), 0},
		{"single byte", []byte("a"), 0},
		{"all same", []byte("aaaaa"), 0},
		{"two equiprobable", []byte("ab"), 1.0},
		{"uniform 4 chars", []byte("abcd"), 2.0},
		{"AKIA example", []byte("AKIAIOSFODNN7EXAMPLE"), 3.6843},
		{"base64-ish high entropy", []byte("3xq8Z1nQpvP7tk+aWoH4mc5XyL2jKBVe"), 5.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShannonEntropy(tt.in)
			if math.Abs(got-tt.want) > 0.01 {
				t.Errorf("ShannonEntropy(%q) = %v, want ~%v", tt.in, got, tt.want)
			}
		})
	}
}

func TestSeverityFromString(t *testing.T) {
	tests := map[string]struct {
		in      string
		want    v1.Severity
		wantErr bool
	}{
		"low":      {"low", v1.Severity_LOW, false},
		"medium":   {"medium", v1.Severity_MEDIUM, false},
		"high":     {"high", v1.Severity_HIGH, false},
		"critical": {"critical", v1.Severity_CRITICAL, false},
		"unknown":  {"FATAL", v1.Severity_SEVERITY_UNSPECIFIED, true},
		"empty":    {"", v1.Severity_SEVERITY_UNSPECIFIED, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := SeverityFromString(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("SeverityFromString(%q) err=%v, wantErr=%v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("SeverityFromString(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
