package scan

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateScanID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		ok   bool
	}{
		{"uuid-like", "550e8400-e29b-41d4-a716-446655440000", true},
		{"short-alnum", "scan-1", true},
		{"underscores", "client_2026_05_25_a", true},
		{"empty", "", false},
		{"wildcard star", "scan-*", false},
		{"wildcard greater", "scan->", false},
		{"dot injection", "scan.id", false},
		{"space", "scan id", false},
		{"slash", "scan/id", false},
		{"newline", "scan\nid", false},
		{"unicode", "сканер", false},
		{"too long", strings.Repeat("a", 129), false},
		{"max length", strings.Repeat("a", 128), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateScanID(tc.id)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
