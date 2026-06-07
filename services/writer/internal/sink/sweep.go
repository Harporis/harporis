// Sweep stale tempfiles left behind by a writer crash mid-flush. The
// accumulator sinks (SARIF/HTML/XLSX/PDF) write to a tempfile then
// atomically rename onto the final path; if the process dies between
// those two steps the tempfile is orphaned. NDJSON appends in place,
// so it has no tempfile to clean up.
//
// Patterns matched:
//   - os.CreateTemp output:   "<scan_id>.{sarif,html}.tmp-<rand>"
//   - crypto/rand mint:       ".<scan_id>.<hex>.{xlsx,pdf}"
//
// Both shapes are unambiguously tempfiles — real outputs are
// "<scan_id>.{ndjson,sarif,html,xlsx,pdf}" without the `.tmp-` infix
// or dot-prefix — so this sweep never deletes a real report.

package sink

import (
	"os"
	"path/filepath"
	"strings"
)

// SweepOrphanTempfiles walks rootDir once and removes any files
// matching the two known tempfile patterns. Returns the number of
// files removed and the first error encountered (subsequent errors
// are logged via the caller-provided onError; missing-dir is not an
// error — first writer startup hasn't created the dir yet).
func SweepOrphanTempfiles(rootDir string, onError func(path string, err error)) (int, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !looksLikeOrphanTempfile(name) {
			continue
		}
		p := filepath.Join(rootDir, name)
		if rerr := os.Remove(p); rerr != nil {
			if onError != nil {
				onError(p, rerr)
			}
			if firstErr == nil {
				firstErr = rerr
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}

// looksLikeOrphanTempfile recognises the two tempfile shapes the
// accumulator sinks produce. Be deliberately strict so the matcher
// never deletes a real output file or operator-stored data.
func looksLikeOrphanTempfile(name string) bool {
	// os.CreateTemp shape: "<scan_id>.sarif.tmp-<rand>" / ".html.tmp-<rand>"
	if i := strings.LastIndex(name, ".tmp-"); i > 0 {
		stem := name[:i]
		if strings.HasSuffix(stem, ".sarif") || strings.HasSuffix(stem, ".html") {
			return true
		}
	}
	// crypto/rand mint shape: ".<scan_id>.<hex>.xlsx" / ".pdf"
	// Must start with '.', have at least two more '.' segments before
	// the extension, and end in .xlsx or .pdf.
	if strings.HasPrefix(name, ".") {
		if strings.HasSuffix(name, ".xlsx") || strings.HasSuffix(name, ".pdf") {
			// Count interior dots so we don't match e.g. ".hidden.xlsx".
			// Need at least: .<scan_id>.<hex>.<ext>  -> 3 dots total.
			if strings.Count(name, ".") >= 3 {
				return true
			}
		}
	}
	return false
}
