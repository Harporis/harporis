// SQLite sink. Streams findings into ONE shared SQLite database at
// <rootDir>/findings.db so operators can run cross-scan queries:
//
//   sqlite3 findings/findings.db \
//     "SELECT scan_id, COUNT(*) FROM findings WHERE severity='CRITICAL'
//      GROUP BY scan_id ORDER BY 2 DESC LIMIT 10;"
//
// Pure-Go driver (modernc.org/sqlite) keeps the writer's
// CGO_ENABLED=0 build target intact. WAL mode lets multiple writer
// replicas share the same DB file with per-INSERT locking instead of
// whole-DB locking.
//
// Streaming model:
//   * On the first Write the sink lazily opens the DB, creates the
//     schema, and prepares the INSERT statement.
//   * Each subsequent Write runs ExecContext with the prepared
//     statement — O(1) amortised.
//   * The schema's PRIMARY KEY (scan_id, finding_id) makes the sink
//     idempotent: a Finding redelivered after a writer crash gets
//     INSERTed once via OR IGNORE.
//   * No per-scan tempfile/rename — every Write commits to the live
//     DB immediately. Finalize is a no-op (Sink interface compliance
//     only — the SQL sink has nothing scan-scoped to close).
//
// Schema mirrors the Parquet sink's flat columns so an operator can
// move between formats without rethinking the data shape.

package sink

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
	"github.com/Harporis/harporis/services/writer/internal/metrics"

	_ "modernc.org/sqlite"
)

const sqliteSinkLabel = "sqlite_file"

// SQLiteDefaultDBName is the filename inside rootDir. Hard-coded; the
// rootDir is operator-configurable.
const SQLiteDefaultDBName = "findings.db"

// SQLite emits a shared findings.db inside rootDir. One *sql.DB per
// sink instance; per-writer-replica replicas just share the same DB
// file via WAL.
type SQLite struct {
	rootDir   string
	dbPath    string
	replicaID string

	mu       sync.Mutex
	closed   bool
	db       *sql.DB
	insert   *sql.Stmt
	rowCount int
}

// NewSQLite constructs a SQLite sink rooted at rootDir. The DB file is
// created on first Write so an idle writer (no scans) doesn't leave
// stray empty files in the bind-mount.
func NewSQLite(rootDir string) (*SQLite, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	metrics.Init()
	return &SQLite{
		rootDir: rootDir,
		dbPath:  filepath.Join(rootDir, SQLiteDefaultDBName),
	}, nil
}

// NewSQLiteConfig accepts a BatchConfig for parity with the other
// sinks. SQLite ignores MaxPerScan / FlushBatch / FlushInterval — each
// Write commits immediately and the on-disk DB is the only state.
func NewSQLiteConfig(rootDir string, _ BatchConfig) (*SQLite, error) {
	return NewSQLite(rootDir)
}

// Name returns the Prometheus sink label.
func (s *SQLite) Name() string { return sqliteSinkLabel }

// SetReplicaID stamps replica_id into the per-replica DB filename when
// multiple writer replicas can't share a single DB file (no
// HARPORIS_FINDINGS_SHARDS sharding). Default empty = one shared
// findings.db across all replicas via WAL. With a non-empty replicaID
// the file becomes findings.<replica>.db so each replica has its own
// store; cross-replica queries: `sqlite3 :memory:` + ATTACH multiple.
func (s *SQLite) SetReplicaID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replicaID = id
	if id == "" {
		s.dbPath = filepath.Join(s.rootDir, SQLiteDefaultDBName)
	} else {
		s.dbPath = filepath.Join(s.rootDir, "findings."+id+".db")
	}
}

// Write inserts one finding into the shared DB. Lazily opens the DB
// on the first call. ScanID is validated client-side (same as every
// other sink) so a malicious producer can't smuggle traversal segments
// into anywhere downstream.
func (s *SQLite) Write(ctx context.Context, f *v1.Finding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	if err := kitscan.ValidateScanID(f.ScanId); err != nil {
		return fmt.Errorf("sink: %w", err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSinkClosed
	}
	if s.db == nil {
		if err := s.openLocked(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	insert := s.insert
	s.mu.Unlock()

	_, err := insert.ExecContext(ctx,
		f.ScanId,
		f.FindingId,
		f.RuleId,
		f.Severity.String(),
		f.FilePath,
		f.LineNumber,
		f.LineNumberEnd,
		f.ByteOffset,
		string(f.MatchedSecret),
		string(f.MatchedLine),
		f.EntropyScore,
		f.DetectedAtMs,
		f.DetectorVersion,
		joinBytesLines(f.ContextBefore),
		joinBytesLines(f.ContextAfter),
	)
	if err != nil {
		return fmt.Errorf("sink: sqlite insert: %w", err)
	}
	s.mu.Lock()
	s.rowCount++
	s.mu.Unlock()
	metrics.SinkWrites.WithLabelValues(sqliteSinkLabel, f.Severity.String()).Inc()
	return nil
}

// Finalize is a no-op for the SQLite sink — every Write already
// committed. Kept on the type so it satisfies the Finalizer interface
// the writer's STATUS consumer dispatches to.
func (s *SQLite) Finalize(_ context.Context, _ string) error { return nil }

// Flush is a no-op for the SQLite sink; included only so callers that
// switch on `interface{ Flush() error }` work uniformly across sinks.
func (s *SQLite) Flush() error { return nil }

// Close closes the prepared INSERT statement + the DB. Idempotent.
func (s *SQLite) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var firstErr error
	if s.insert != nil {
		if err := s.insert.Close(); err != nil {
			firstErr = fmt.Errorf("sink: sqlite stmt close: %w", err)
		}
		s.insert = nil
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sink: sqlite db close: %w", err)
		}
		s.db = nil
		metrics.SinkFlushTotal.WithLabelValues(sqliteSinkLabel, "close").Inc()
	}
	return firstErr
}

// openLocked opens the DB, runs schema migrations, and prepares the
// INSERT statement. Caller must hold s.mu.
func (s *SQLite) openLocked() error {
	// DSN with PRAGMA bundle: WAL for multi-writer concurrency,
	// NORMAL fsync (matches Parquet/SARIF semantics — a kill -9 may
	// lose the last sub-second of writes, full crash-safety needs
	// synchronous=FULL but at significant write throughput cost),
	// busy_timeout so concurrent writers retry briefly instead of
	// surfacing SQLITE_BUSY to the operator.
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(0)")
	dsn := s.dbPath + "?" + q.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("sink: sqlite open: %w", err)
	}
	// SQLite only allows one writer at a time. Cap the pool at 1 so
	// concurrent worker goroutines serialise through one busy_timeout
	// rather than racing inside the driver and surfacing SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(sqliteSchema); err != nil {
		_ = db.Close()
		return fmt.Errorf("sink: sqlite schema: %w", err)
	}
	stmt, err := db.Prepare(sqliteInsert)
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("sink: sqlite prepare: %w", err)
	}
	s.db = db
	s.insert = stmt
	return nil
}

// sqliteSchema bootstraps the `findings` table and the four most
// useful query indexes (per-scan, severity, rule, time). Idempotent —
// CREATE IF NOT EXISTS lets multiple writer replicas open the same
// DB without coordinating who's "first".
const sqliteSchema = `
CREATE TABLE IF NOT EXISTS findings (
  scan_id          TEXT NOT NULL,
  finding_id       TEXT NOT NULL,
  rule_id          TEXT NOT NULL,
  severity         TEXT NOT NULL,
  file_path        TEXT,
  line_number      INTEGER,
  line_number_end  INTEGER,
  byte_offset      INTEGER,
  matched_secret   TEXT,
  matched_line     TEXT,
  entropy_score    REAL,
  detected_at_ms   INTEGER,
  detector_version TEXT,
  context_before   TEXT,
  context_after    TEXT,
  PRIMARY KEY (scan_id, finding_id)
);
CREATE INDEX IF NOT EXISTS findings_by_scan       ON findings(scan_id);
CREATE INDEX IF NOT EXISTS findings_by_severity   ON findings(severity);
CREATE INDEX IF NOT EXISTS findings_by_rule       ON findings(rule_id);
CREATE INDEX IF NOT EXISTS findings_by_detected   ON findings(detected_at_ms);
`

// sqliteInsert: OR IGNORE so a redelivered finding (writer crash
// between INSERT and Ack) gets dropped at the dedup boundary instead
// of returning UNIQUE-violation errors that would Nak the message
// forever.
const sqliteInsert = `
INSERT OR IGNORE INTO findings (
  scan_id, finding_id, rule_id, severity,
  file_path, line_number, line_number_end, byte_offset,
  matched_secret, matched_line, entropy_score,
  detected_at_ms, detector_version,
  context_before, context_after
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
`

// DBPath returns the on-disk path the sink writes to. Useful for
// tests + CLI integration that want to attach the same DB.
func (s *SQLite) DBPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dbPath
}

// SanitizeDBPath returns dbPath without query-string PRAGMAs.
// Exposed for log lines that don't need the full DSN.
func SanitizeDBPath(dbPath string) string {
	if i := strings.IndexByte(dbPath, '?'); i >= 0 {
		return dbPath[:i]
	}
	return dbPath
}
