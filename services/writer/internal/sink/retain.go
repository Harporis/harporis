// Retention sweep for the findings directory. The accumulator sinks and
// NDJSON rotate files per-scan, so on a long-running deployment the
// findings/ mount grows unbounded. This sweep enforces two operator-
// configurable caps:
//
//   - TTL: files older than RetentionPolicy.TTL are deleted regardless
//     of total disk usage.
//   - Size: if the surviving total exceeds RetentionPolicy.MaxBytes,
//     oldest-first deletes continue until the total falls under the
//     cap.
//
// Only files whose names match the known sink-output shape
// (`<scan_id>.<ext>` or `<scan_id>.<replica>.<ext>` where ext is one of
// the six known sink extensions) are eligible — operator-stored files
// in the same dir are never touched. Sub-directories are not recursed
// (no sink writes to them).

package sink

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RetentionPolicy bounds the findings dir. Zero values disable each
// cap independently — TTL=0 means "don't delete by age", MaxBytes=0
// means "don't enforce a size cap".
type RetentionPolicy struct {
	TTL      time.Duration
	MaxBytes int64
}

// Disabled reports whether both caps are zero, in which case the
// sweep is a no-op and callers can skip starting the ticker.
func (p RetentionPolicy) Disabled() bool {
	return p.TTL <= 0 && p.MaxBytes <= 0
}

// SweepStats is the result of one SweepRetention pass.
type SweepStats struct {
	RemovedByAge   int
	RemovedBySize  int
	BytesRemoved   int64
	RemainingFiles int
	RemainingBytes int64
}

// knownSinkExts is the set of real-output file extensions a writer
// replica produces. Anything else (including operator-stored archives
// or unrelated files mounted into the same dir) is ignored by the
// retention sweep.
var knownSinkExts = map[string]struct{}{
	".ndjson":  {},
	".sarif":   {},
	".html":    {},
	".xlsx":    {},
	".pdf":     {},
	".parquet": {},
}

// SweepRetention deletes findings files in rootDir according to policy.
// Returns counts and the first error encountered (subsequent errors
// surface via onError). A missing rootDir is not an error.
func SweepRetention(rootDir string, policy RetentionPolicy, now time.Time, onError func(path string, err error)) (SweepStats, error) {
	var stats SweepStats
	if policy.Disabled() {
		return stats, nil
	}
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return stats, nil
		}
		return stats, err
	}

	type fileMeta struct {
		path  string
		size  int64
		mtime time.Time
	}
	files := make([]fileMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !looksLikeSinkOutput(name) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			if onError != nil {
				onError(filepath.Join(rootDir, name), err)
			}
			continue
		}
		files = append(files, fileMeta{
			path:  filepath.Join(rootDir, name),
			size:  fi.Size(),
			mtime: fi.ModTime(),
		})
	}
	// Oldest first — both TTL and size passes want this order.
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime.Before(files[j].mtime)
	})

	survivors := files[:0]
	// Age pass.
	if policy.TTL > 0 {
		cutoff := now.Add(-policy.TTL)
		for _, fm := range files {
			if fm.mtime.Before(cutoff) {
				if err := os.Remove(fm.path); err != nil {
					if onError != nil {
						onError(fm.path, err)
					}
					survivors = append(survivors, fm)
					continue
				}
				stats.RemovedByAge++
				stats.BytesRemoved += fm.size
				continue
			}
			survivors = append(survivors, fm)
		}
	} else {
		survivors = append(survivors, files...)
	}

	// Size pass against survivors only.
	if policy.MaxBytes > 0 {
		var total int64
		for _, fm := range survivors {
			total += fm.size
		}
		i := 0
		for total > policy.MaxBytes && i < len(survivors) {
			fm := survivors[i]
			if err := os.Remove(fm.path); err != nil {
				if onError != nil {
					onError(fm.path, err)
				}
				i++
				continue
			}
			total -= fm.size
			stats.RemovedBySize++
			stats.BytesRemoved += fm.size
			i++
		}
		survivors = survivors[i:]
		stats.RemainingBytes = total
	} else {
		for _, fm := range survivors {
			stats.RemainingBytes += fm.size
		}
	}
	stats.RemainingFiles = len(survivors)
	return stats, nil
}

// looksLikeSinkOutput returns true for files matching real sink-output
// naming. Real outputs are `<scan_id>.<ext>` or
// `<scan_id>.<replica>.<ext>` — never starting with '.', never with
// `.tmp-` in them.
func looksLikeSinkOutput(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	if strings.Contains(name, ".tmp-") {
		return false
	}
	ext := filepath.Ext(name)
	if ext == "" {
		return false
	}
	_, ok := knownSinkExts[strings.ToLower(ext)]
	return ok
}
