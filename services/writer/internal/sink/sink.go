// Package sink defines the writer's output destinations. v0.1 ships one
// implementation: NDJSON file-per-scan, suitable for piping through `jq`
// or post-processing tools. SARIF and other sinks are deferred to v0.2.
package sink

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
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

// ErrSinkClosed is returned by Write after Close has run.
var ErrSinkClosed = errors.New("sink: closed")

// DefaultMaxOpenFiles bounds how many *os.File handles the NDJSON sink
// holds open simultaneously. Past this limit, the least-recently-used
// scan's file is Synced + Closed; a future Write for that scan re-opens
// (O_APPEND preserves data). Tuned conservatively against the typical
// container RLIMIT_NOFILE of 1024.
const DefaultMaxOpenFiles = 512

// NDJSONFile writes one JSON-encoded Finding per line to
// <rootDir>/<scan_id>.ndjson. One *os.File per scan_id is held open
// (bounded by maxOpen via LRU eviction). The file is opened with O_APPEND
// so multiple writer replicas sharing the directory get kernel-linearized
// write(2) calls up to PIPE_BUF (typically 4096 bytes).
type NDJSONFile struct {
	rootDir string
	maxOpen int

	mu      sync.Mutex
	files   map[string]*list.Element // scanID → element holding *scanFile
	lru     *list.List               // front = most recently used
	closed  bool
}

type scanFile struct {
	mu     sync.Mutex
	f      *os.File
	scanID string
}

// NewNDJSONFile constructs a sink rooted at rootDir. The directory is
// created if it doesn't exist (mode 0o755). The sink will keep at most
// maxOpen files open at a time; pass 0 to use DefaultMaxOpenFiles.
func NewNDJSONFile(rootDir string) (*NDJSONFile, error) {
	return NewNDJSONFileN(rootDir, DefaultMaxOpenFiles)
}

// NewNDJSONFileN is NewNDJSONFile with an explicit fd cap. Exposed for
// tests that want to exercise eviction paths with small caps.
func NewNDJSONFileN(rootDir string, maxOpen int) (*NDJSONFile, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if maxOpen <= 0 {
		maxOpen = DefaultMaxOpenFiles
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	return &NDJSONFile{
		rootDir: rootDir,
		maxOpen: maxOpen,
		files:   make(map[string]*list.Element),
		lru:     list.New(),
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
// single write(2) call. ctx is consulted before file-handle resolution
// and before the actual write — cancellation during the write itself is
// not preempted because os.File.Write does not honour context.
func (n *NDJSONFile) Write(ctx context.Context, f *v1.Finding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	// SECURITY: scan_id is attacker-controlled (anyone with NATS publish
	// access can craft a Finding) and is used verbatim in the output
	// path below. Reject anything that doesn't match the shared
	// allowlist BEFORE building the path; the containment check in
	// acquire() is belt-and-suspenders.
	if err := kitscan.ValidateScanID(f.ScanId); err != nil {
		return fmt.Errorf("sink: %w", err)
	}
	data, err := jsonMarshaller.Marshal(f)
	if err != nil {
		return fmt.Errorf("sink: marshal finding %s: %w", f.FindingId, err)
	}
	// One write call: append the newline into the (slack-capacity) slice
	// protojson returned. If the slice has no spare cap, this allocates;
	// in practice protojson over-allocates so the append is in-place.
	line := append(data, '\n')

	// acquire returns sf with its mu LOCKED if successful. This atomic
	// "find-or-open and lock" pattern is what fixes the Close-vs-Write
	// race: Close cannot evict a file we already hold the lock on, and
	// post-Close acquire fast-fails on n.closed.
	sf, err := n.acquire(f.ScanId)
	if err != nil {
		return err
	}
	defer sf.mu.Unlock()
	if _, err := sf.f.Write(line); err != nil {
		return fmt.Errorf("sink: write scan %s: %w", f.ScanId, err)
	}
	return nil
}

// acquire returns sf with sf.mu LOCKED. Callers MUST Unlock. Returns
// ErrSinkClosed once Close has run.
func (n *NDJSONFile) acquire(scanID string) (*scanFile, error) {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil, ErrSinkClosed
	}
	if el, ok := n.files[scanID]; ok {
		n.lru.MoveToFront(el)
		sf := el.Value.(*scanFile)
		// Lock sf.mu BEFORE releasing n.mu so Close (which takes n.mu
		// then walks the LRU) cannot Close the file under us.
		sf.mu.Lock()
		n.mu.Unlock()
		return sf, nil
	}
	// Miss: enforce cap by evicting the LRU tail (with its own lock,
	// held briefly under n.mu). Eviction Close-syncs the file; a
	// subsequent Write for that scan will re-open via O_APPEND.
	for len(n.files) >= n.maxOpen && n.lru.Len() > 0 {
		oldest := n.lru.Back()
		n.lru.Remove(oldest)
		ev := oldest.Value.(*scanFile)
		delete(n.files, ev.scanID)
		ev.mu.Lock()
		_ = ev.f.Sync()
		_ = ev.f.Close()
		ev.mu.Unlock()
	}
	path := filepath.Join(n.rootDir, scanID+".ndjson")
	// SECURITY: belt-and-suspenders containment — ValidateScanID upstream
	// already rejects anything that could traverse, but a future bug
	// (callers bypassing acquire()? validator regression?) must not
	// silently escape rootDir. filepath.Clean has already normalized
	// path; require it to live under rootDir + separator.
	rootClean := filepath.Clean(n.rootDir)
	if !strings.HasPrefix(filepath.Clean(path), rootClean+string(filepath.Separator)) {
		n.mu.Unlock()
		return nil, fmt.Errorf("sink: path %q escapes rootDir %q", path, n.rootDir)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		n.mu.Unlock()
		return nil, fmt.Errorf("sink: open %s: %w", path, err)
	}
	sf := &scanFile{f: f, scanID: scanID}
	el := n.lru.PushFront(sf)
	n.files[scanID] = el
	sf.mu.Lock()
	n.mu.Unlock()
	return sf, nil
}

// Close flushes and releases all open scan files. Idempotent: subsequent
// Close calls return nil, and any Write that arrives after Close returns
// ErrSinkClosed (no silent file re-open).
func (n *NDJSONFile) Close() error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil
	}
	n.closed = true
	files := n.files
	n.files = nil
	n.lru = list.New()
	n.mu.Unlock()

	var firstErr error
	for id, el := range files {
		sf := el.Value.(*scanFile)
		sf.mu.Lock()
		if err := sf.f.Sync(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sink: sync %s: %w", id, err)
		}
		if err := sf.f.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sink: close %s: %w", id, err)
		}
		sf.mu.Unlock()
	}
	return firstErr
}
