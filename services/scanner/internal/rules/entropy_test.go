package rules

import (
	"math"
	"testing"
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
		wantErr bool
	}{
		"low":      {"low", false},
		"medium":   {"medium", false},
		"high":     {"high", false},
		"critical": {"critical", false},
		"unknown":  {"FATAL", true},
		"empty":    {"", true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := SeverityFromString(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("SeverityFromString(%q) err=%v, wantErr=%v", tt.in, err, tt.wantErr)
			}
		})
	}
}
