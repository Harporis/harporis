// Reconstructs a single accumulator/streaming sink output from the
// authoritative NDJSON file. Operator escape hatch for the case where
// SARIF/HTML/XLSX/PDF/Parquet went stale (writer crashed mid-flush,
// last batch lost) and they want a clean re-render without restarting
// the whole pipeline.
//
// Reads <input-dir>/<scan_id>.ndjson, decodes each line back into a
// Finding, replays it through the requested sink, and finalizes.
// Output lands at <output-dir>/<scan_id>.<ext> — no replica-id suffix
// (one-shot rebuild has no replica to disambiguate against).
//
// NDJSON itself cannot be a rebuild target — it IS the source of truth.

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/severity"
	kitscan "github.com/Harporis/harporis/kit/scan"
	"github.com/Harporis/harporis/services/writer/internal/sink"
)

type rebuildSink interface {
	sink.Sink
	Finalize(ctx context.Context, scanID string) error
}

func main() {
	scanID := flag.String("scan-id", "", "scan_id whose NDJSON to replay (required)")
	format := flag.String("format", "", "target format: sarif|html|xlsx|pdf|parquet (required)")
	sevCSV := flag.String("severity", "", "comma-separated severity levels to KEEP (e.g. CRITICAL,HIGH); empty = all")
	inputDir := flag.String("input-dir", "/var/lib/harporis/findings", "directory holding <scan_id>.ndjson")
	outputDir := flag.String("output-dir", "", "destination directory; defaults to --input-dir")
	flag.Parse()

	if *scanID == "" || *format == "" {
		fail("usage: writer-rebuild --scan-id X --format {sarif|html|xlsx|pdf|parquet} [--severity LEVELS] [--input-dir D] [--output-dir D]")
	}
	if err := kitscan.ValidateScanID(*scanID); err != nil {
		fail("invalid --scan-id: %v", err)
	}
	sevSet, err := severity.ParseCSV(*sevCSV)
	if err != nil {
		fail("invalid --severity: %v", err)
	}
	if *outputDir == "" {
		*outputDir = *inputDir
	}

	ndjsonPath := filepath.Join(*inputDir, *scanID+".ndjson")
	f, err := os.Open(ndjsonPath)
	if err != nil {
		fail("open ndjson %s: %v", ndjsonPath, err)
	}
	defer f.Close()

	out, err := buildSink(*format, *outputDir)
	if err != nil {
		fail("init sink: %v", err)
	}

	ctx := context.Background()
	count, err := replay(ctx, f, out, *scanID, sevSet)
	if err != nil {
		_ = out.Close()
		fail("replay: %v", err)
	}
	if err := out.Finalize(ctx, *scanID); err != nil {
		_ = out.Close()
		fail("finalize: %v", err)
	}
	if err := out.Close(); err != nil {
		fail("close: %v", err)
	}
	fmt.Printf("rebuilt %s sink for scan %s: %d findings → %s\n", *format, *scanID, count, *outputDir)
}

func buildSink(format, dir string) (rebuildSink, error) {
	// Sync-flush config — single replay pass, no benefit from batching.
	cfg := sink.BatchConfig{FlushBatch: 1}
	switch strings.ToLower(format) {
	case "sarif":
		return sink.NewSARIFConfig(dir, cfg)
	case "html":
		return sink.NewHTMLConfig(dir, cfg)
	case "xlsx":
		return sink.NewXLSXConfig(dir, cfg)
	case "pdf":
		return sink.NewPDFConfig(dir, cfg)
	case "parquet":
		return sink.NewParquetConfig(dir, cfg)
	default:
		return nil, fmt.Errorf("unsupported format %q (want sarif|html|xlsx|pdf|parquet; ndjson is the source, can't be a target)", format)
	}
}

func replay(ctx context.Context, r io.Reader, out rebuildSink, scanID string, sevSet severity.Set) (int, error) {
	sc := bufio.NewScanner(r)
	// Findings can carry full context windows — bump the scan buffer
	// well past the default 64 KiB so we don't choke on a wide line.
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	um := protojson.UnmarshalOptions{DiscardUnknown: true}
	// count tracks findings WRITTEN; lineNum tracks the physical NDJSON
	// line so error messages stay accurate even when --severity skips
	// some lines (count would otherwise undercount the real position).
	var count int
	var lineNum int
	for sc.Scan() {
		lineNum++
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var f v1.Finding
		if err := um.Unmarshal(line, &f); err != nil {
			return count, fmt.Errorf("decode line %d: %w", lineNum, err)
		}
		if f.ScanId != scanID {
			return count, fmt.Errorf("line %d carries scan_id %q, expected %q (mixed file?)", lineNum, f.ScanId, scanID)
		}
		if !sevSet.Contains(f.Severity) {
			continue
		}
		if err := out.Write(ctx, &f); err != nil {
			return count, fmt.Errorf("sink write at line %d: %w", lineNum, err)
		}
		count++
	}
	if err := sc.Err(); err != nil {
		return count, fmt.Errorf("read ndjson: %w", err)
	}
	return count, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
