package detect

import (
	"time"

	"github.com/google/uuid"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/scanner/internal/rules"
)

// Detector applies a fixed slice of rules to GitRowChunks. Stateless and
// safe for concurrent use across goroutines (rules are read-only).
type Detector struct {
	rules           []rules.Rule
	detectorVersion string
	now             func() time.Time
}

func NewDetector(rs []rules.Rule, detectorVersion string) *Detector {
	return &Detector{rules: rs, detectorVersion: detectorVersion, now: time.Now}
}

// ScanChunk returns all findings produced by applying every rule to the
// chunk's joined text. May return nil/empty when no rule matches.
func (d *Detector) ScanChunk(c *v1.GitRowChunk) []*v1.Finding {
	if len(c.Rows) == 0 {
		return nil
	}
	// Build joined text + line index.
	rowLens := make([]int, len(c.Rows))
	totalLen := 0
	for i, r := range c.Rows {
		rowLens[i] = len(r.Content)
		totalLen += len(r.Content) + 1 // +1 for join newline
	}
	joined := make([]byte, 0, totalLen)
	for i, r := range c.Rows {
		if i > 0 {
			joined = append(joined, '\n')
		}
		joined = append(joined, r.Content...)
	}
	idx := NewLineIndex(rowLens)

	var findings []*v1.Finding
	for _, rule := range d.rules {
		matches := rule.Regex.FindAllSubmatchIndex(joined, -1)
		for _, m := range matches {
			// m[0]:m[1] is full match; m[2*i]:m[2*i+1] is capture group i (if >0).
			start, end := m[0], m[1]
			matched := joined[start:end]

			// Entropy filter (if rule has one).
			var entropy float64
			if rule.EntropyMin > 0 {
				targetStart, targetEnd := start, end
				gi := rule.EntropyGrp
				if gi > 0 && 2*gi+1 < len(m) && m[2*gi] >= 0 {
					targetStart, targetEnd = m[2*gi], m[2*gi+1]
				}
				entropy = rules.ShannonEntropy(joined[targetStart:targetEnd])
				if entropy < rule.EntropyMin {
					continue
				}
			} else {
				entropy = 0
			}

			startLine := idx.LineAt(start)
			endLine := idx.LineAt(end - 1)
			// Map back to 1-based line numbers from the chunk's rows (rows[i].LineNumber).
			lineNumber := c.Rows[startLine].LineNumber
			lineNumberEnd := c.Rows[endLine].LineNumber

			// matched_line: full content of the starting line.
			matchedLine := c.Rows[startLine].Content

			f := &v1.Finding{
				ScanId:          c.ScanId,
				FindingId:       uuid.NewString(),
				ChunkId:         c.ChunkId,
				RuleId:          rule.ID,
				Severity:        rule.Severity,
				LineNumber:      lineNumber,
				LineNumberEnd:   lineNumberEnd,
				ByteOffset:      int64(start - idx.starts[startLine]),
				MatchedSecret:   append([]byte(nil), matched...),
				MatchedLine:     append([]byte(nil), matchedLine...),
				EntropyScore:    entropy,
				DetectedAtMs:    d.now().UnixMilli(),
				DetectorVersion: d.detectorVersion,
			}
			switch c.Kind {
			case v1.ChunkKind_DIFF_WINDOW:
				f.FilePath = c.FilePath
				f.CommitSha = c.CommitSha
			case v1.ChunkKind_BLOB:
				f.Refs = c.Refs
			}
			findings = append(findings, f)
		}
	}
	return findings
}
