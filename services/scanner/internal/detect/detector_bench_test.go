package detect

import (
	"fmt"
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/scanner/internal/rules"
)

// benchChunk builds a synthetic GitRowChunk with `lines` rows of
// `bytesPerLine` bytes each. Roughly one in 50 lines contains a string
// that looks plausibly close to a secret so the hot loop exercises both
// the no-match and match paths.
func benchChunk(lines, bytesPerLine int) *v1.GitRowChunk {
	rows := make([]*v1.GitRow, lines)
	filler := strings.Repeat("x", bytesPerLine)
	for i := range lines {
		var content string
		switch i % 50 {
		case 7:
			content = "aws_key = AKIAIOSFODNN7EXAMPLE"
		case 23:
			content = `password = "p@ssw0rdHotPotat0123"`
		default:
			content = filler
		}
		rows[i] = &v1.GitRow{
			LineNumber: int32(i + 1),
			ByteOffset: 0,
			Content:    []byte(content),
		}
	}
	return &v1.GitRowChunk{
		ScanId:   "bench",
		ChunkId:  fmt.Sprintf("chunk-%d-%d", lines, bytesPerLine),
		Kind:     v1.ChunkKind_BLOB,
		FilePath: "bench.txt",
		Rows:     rows,
	}
}

// loadDefaultRules pulls the embedded production rule pack so the
// benchmark exercises the real 28-rule workload. Skips the benchmark
// gracefully if the pack fails to load (CI sanity).
func loadDefaultRules(b *testing.B) []rules.Rule {
	b.Helper()
	rs, err := rules.LoadEmbedded()
	if err != nil {
		b.Fatalf("load embedded rules: %v", err)
	}
	return rs
}

// BenchmarkScanChunk_DefaultPack exercises the production rule pack
// against synthetic chunks of varying size. Use as the baseline before
// landing any regex-pack consolidation work.
//
//	go test -bench=ScanChunk_DefaultPack -benchmem -count=3 \
//	  ./internal/detect/...
func BenchmarkScanChunk_DefaultPack(b *testing.B) {
	rs := loadDefaultRules(b)
	d := NewDetector(rs, "bench")

	cases := []struct {
		name         string
		lines        int
		bytesPerLine int
	}{
		{"small_64x80", 64, 80},
		{"medium_512x80", 512, 80},
		{"large_2048x80", 2048, 80},
		{"wide_256x320", 256, 320},
	}
	for _, tc := range cases {
		c := benchChunk(tc.lines, tc.bytesPerLine)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = d.ScanChunk(c)
			}
		})
	}
}

// BenchmarkScanChunk_SingleRule isolates the per-rule cost so the
// regex-pack consolidation work can be evaluated on a clean baseline
// (a single rule pass = no per-rule overhead).
func BenchmarkScanChunk_SingleRule(b *testing.B) {
	d := NewDetector([]rules.Rule{ruleAKIA()}, "bench")
	c := benchChunk(2048, 80)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.ScanChunk(c)
	}
}
