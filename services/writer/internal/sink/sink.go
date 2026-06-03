// Package sink defines the writer's output destinations. v0.1 ships one
// implementation: NDJSON file-per-scan, suitable for piping through `jq`
// or post-processing tools. SARIF and other sinks are deferred to v0.2.
package sink

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Sink absorbs a Finding into a durable destination. Implementations MUST
// be safe for concurrent calls from worker goroutines and MUST flush
// per-Write so a crash never loses an Ack'd finding.
type Sink interface {
	// Write persists exactly one Finding. Returning an error tells the
	// caller to Nak the JetStream message; returning nil tells it to Ack.
	Write(ctx context.Context, f *v1.Finding) error
	// Close flushes and releases all held file handles. Idempotent.
	Close() error
	// Name is used as the `sink` label on Prometheus collectors.
	Name() string
}

// NDJSONFile writes one JSON-encoded Finding per line to
// <rootDir>/<scan_id>.ndjson. One *os.File per scan_id is held open across
// calls; a per-scan mutex serializes writes within the file while letting
// distinct scans proceed in parallel. The file is opened with O_APPEND so
// even if two writer replicas share the directory the kernel will linearize
// individual write(2) calls up to PIPE_BUF (typically 4096 bytes).
type NDJSONFile struct {
	rootDir string

	mu    sync.Mutex
	files map[string]*scanFile
}

type scanFile struct {
	mu sync.Mutex
	f  *os.File
}

// NewNDJSONFile constructs a sink rooted at rootDir. The directory is
// created if it doesn't exist (mode 0o755). Returns an error if the path
// exists but isn't a writable directory.
func NewNDJSONFile(rootDir string) (*NDJSONFile, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	return &NDJSONFile{
		rootDir: rootDir,
		files:   make(map[string]*scanFile),
	}, nil
}

// Name returns the sink identifier used as a Prometheus label.
func (n *NDJSONFile) Name() string { return "ndjson_file" }

// jsonMarshaller is a package-level singleton. UseProtoNames keeps field
// names matching the .proto file (e.g. "scan_id" not "scanId") which is
// what tools like `jq` and ndjson-grep expect.
var jsonMarshaller = protojson.MarshalOptions{
	UseProtoNames:   true,
	EmitUnpopulated: false,
}

// Write encodes f as JSON, appends a trailing newline, and writes it as a
// single write(2) call. ctx is consulted before the file handle resolution
// and before the actual write — cancellation during the write itself is not
// preempted because os.File.Write does not honour context.
func (n *NDJSONFile) Write(ctx context.Context, f *v1.Finding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	if f.ScanId == "" {
		return fmt.Errorf("sink: Finding.scan_id is empty")
	}
	data, err := jsonMarshaller.Marshal(f)
	if err != nil {
		return fmt.Errorf("sink: marshal finding %s: %w", f.FindingId, err)
	}
	// Buffer the line (data + '\n') so we issue ONE write call.
	line := make([]byte, 0, len(data)+1)
	line = append(line, data...)
	line = append(line, '\n')

	sf, err := n.fileFor(f.ScanId)
	if err != nil {
		return err
	}
	sf.mu.Lock()
	defer sf.mu.Unlock()
	if _, err := sf.f.Write(line); err != nil {
		return fmt.Errorf("sink: write scan %s: %w", f.ScanId, err)
	}
	return nil
}

// fileFor returns (creating if necessary) the open file for a scan id.
// The returned *scanFile's mutex is NOT held — callers acquire it.
func (n *NDJSONFile) fileFor(scanID string) (*scanFile, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if sf, ok := n.files[scanID]; ok {
		return sf, nil
	}
	path := filepath.Join(n.rootDir, scanID+".ndjson")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("sink: open %s: %w", path, err)
	}
	sf := &scanFile{f: f}
	n.files[scanID] = sf
	return sf, nil
}

// Close flushes and releases all open scan files. Safe to call multiple
// times — subsequent calls find an empty file map and return nil.
func (n *NDJSONFile) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	var firstErr error
	for id, sf := range n.files {
		sf.mu.Lock()
		if err := sf.f.Sync(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sink: sync %s: %w", id, err)
		}
		if err := sf.f.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sink: close %s: %w", id, err)
		}
		sf.mu.Unlock()
	}
	n.files = make(map[string]*scanFile)
	return firstErr
}
