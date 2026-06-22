package findings

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Harporis/harporis/services/cli/internal/compose"
)

// Load reads and parses the scan's NDJSON findings report.
func Load(scanID, outputDir string) ([]Finding, error) {
	body, err := ReadFile(scanID, ".ndjson", outputDir)
	if err != nil {
		return nil, err
	}
	return Parse(strings.NewReader(body))
}

// ReadFile returns the contents of the scan's <ext> report,
// either from a host directory (--output-dir) or via
// `docker compose exec writer cat`. It tolerates the replica suffix the
// writer adds in sharded mode (<scan>.<replicahash><ext>), so proxy
// formats (pdf/html/sarif/xlsx/parquet) resolve the same as ndjson.
func ReadFile(scanID, ext, outputDir string) (string, error) {
	if outputDir != "" {
		entries, err := os.ReadDir(outputDir)
		if err != nil {
			return "", fmt.Errorf("read dir %s: %w", outputDir, err)
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		name := resolveFindingsName(names, scanID, ext)
		if name == "" {
			return "", noReportErr(scanID, ext, names)
		}
		b, err := os.ReadFile(outputDir + "/" + name)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		return string(b), nil
	}
	const dir = "/var/lib/harporis/findings"
	co, err := compose.NewDefault()
	if err != nil {
		return "", fmt.Errorf("docker compose not available: %w (pass --output-dir for host file access)", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	listing, err := co.Exec(ctx, "writer", "ls", "-1", dir)
	if err != nil {
		detail := strings.TrimSpace(listing)
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("compose exec writer ls %s: %s", dir, detail)
	}
	var names []string
	for _, n := range strings.Split(listing, "\n") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	name := resolveFindingsName(names, scanID, ext)
	if name == "" {
		return "", noReportErr(scanID, ext, names)
	}
	path := dir + "/" + name
	body, err := co.Exec(ctx, "writer", "cat", path)
	if err != nil {
		detail := strings.TrimSpace(body)
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("compose exec writer cat %s: %s", path, detail)
	}
	return body, nil
}

// ResolveFindingsName picks the on-disk filename for a scan's <ext> report
// out of a directory listing. The writer writes a bare <scan><ext> in the
// legacy single-pool mode, but a replica-suffixed <scan>.<replicahash><ext>
// when HARPORIS_FINDINGS_SHARDS=1. The bare name is preferred; a suffixed
// match is the fallback. Tempfiles (<scan>.<ext>.tmp-*) and other formats
// are ignored. Returns "" when neither form is present.
//
// Exported so cmd package tests can exercise the resolution logic directly.
func ResolveFindingsName(names []string, scanID, ext string) string {
	return resolveFindingsName(names, scanID, ext)
}

// resolveFindingsName is the internal implementation.
func resolveFindingsName(names []string, scanID, ext string) string {
	bare := scanID + ext
	if slices.Contains(names, bare) {
		return bare
	}
	// Suffixed: <scan>.<hash><ext>, e.g. <scan>.3bad9a0183a5.pdf. The char
	// after scanID must be '.' so we never match a different scan_id that
	// merely shares this one as a prefix.
	for _, n := range names {
		if strings.HasPrefix(n, scanID+".") && strings.HasSuffix(n, ext) {
			return n
		}
	}
	return ""
}

// availableFindingsExts lists the distinct report extensions present for a
// scan (bare or replica-suffixed), so a "no <fmt> report" error can tell
// the operator which formats *are* available. Tempfiles are skipped.
func availableFindingsExts(names []string, scanID string) []string {
	seen := map[string]struct{}{}
	var exts []string
	for _, n := range names {
		if !strings.HasPrefix(n, scanID+".") {
			continue
		}
		ext := n[strings.LastIndexByte(n, '.'):]
		if strings.Contains(ext, "tmp") {
			continue
		}
		if _, ok := seen[ext]; ok {
			continue
		}
		seen[ext] = struct{}{}
		exts = append(exts, ext)
	}
	slices.Sort(exts)
	return exts
}

// noReportErr builds the clear "no <ext> report for this scan" error,
// listing the formats that do exist instead of a raw `cat: No such file`.
func noReportErr(scanID, ext string, names []string) error {
	avail := availableFindingsExts(names, scanID)
	if len(avail) == 0 {
		return fmt.Errorf("no findings for scan %s (unknown scan_id, or the writer hasn't materialized it yet)", scanID)
	}
	return fmt.Errorf("no %s report for scan %s — sink disabled or excluded by per-scan -f; available: %s",
		strings.TrimPrefix(ext, "."), scanID, strings.Join(avail, ", "))
}
