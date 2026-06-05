// Package rulewatch implements rules hot-reload. Polls the rule-pack file's mtime on a ticker and
// atomically swaps a new detector into place when a change is detected.
// Subsequent scans pick up the new rules without restart; in-flight
// chunks finish on whatever detector they started with (no mid-chunk
// inconsistency).
//
// We poll instead of using fsnotify so the scanner stays self-contained
// (no platform-specific deps) and works with bind-mounted volumes where
// fsnotify is unreliable.

package rulewatch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/Harporis/harporis/services/scanner/internal/detect"
	"github.com/Harporis/harporis/services/scanner/internal/rules"
)

// Watcher holds a swappable *detect.Detector and reloads it from a YAML
// rule pack on file change. Goroutine-safe; Current() returns the
// latest detector pointer without blocking.
type Watcher struct {
	path            string
	detectorVersion string
	current         atomic.Pointer[detect.Detector]
	rulesCount      atomic.Int64
	lastMtime       atomic.Int64 // unix nanos of last seen mtime
	reloads         atomic.Int64 // counts successful reloads since start
}

// NewWatcher builds a watcher around path. The initial load happens
// before Run starts so Current() is never nil after a successful
// construction. Returns an error if the initial load fails.
func NewWatcher(path, detectorVersion string) (*Watcher, error) {
	w := &Watcher{path: path, detectorVersion: detectorVersion}
	if err := w.reload(); err != nil {
		return nil, fmt.Errorf("initial rules load: %w", err)
	}
	return w, nil
}

// Current returns the current detector. Safe to call from many goroutines.
func (w *Watcher) Current() *detect.Detector { return w.current.Load() }

// RulesCount returns the number of rules in the currently loaded pack.
func (w *Watcher) RulesCount() int { return int(w.rulesCount.Load()) }

// Reloads returns the number of successful reloads since the watcher
// was constructed (initial load excluded — that's reload #0).
func (w *Watcher) Reloads() int { return int(w.reloads.Load()) }

// Run blocks until ctx is cancelled. Polls the file mtime every
// interval; on change, loads + validates + atomic-swaps the detector.
// Load/validate failures are logged at Warn (the old detector keeps
// serving) — operator's mistake shouldn't take the scanner offline.
func (w *Watcher) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.checkAndReload(); err != nil {
				slog.Warn("rules reload failed (keeping previous pack)",
					"path", w.path, "err", err,
				)
			}
		}
	}
}

// checkAndReload stats the file; if mtime changed since the last
// successful load, it reloads. Returns the load error or nil.
func (w *Watcher) checkAndReload() error {
	fi, err := os.Stat(w.path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	mtime := fi.ModTime().UnixNano()
	if mtime == w.lastMtime.Load() {
		return nil // unchanged
	}
	if err := w.reload(); err != nil {
		return err
	}
	w.reloads.Add(1)
	slog.Info("rules reloaded", "path", w.path, "rules", w.RulesCount(), "reload_n", w.Reloads())
	return nil
}

// reload loads + validates the rule pack and atomically swaps the
// detector. Returns the load error without mutating state on failure.
func (w *Watcher) reload() error {
	pack, err := rules.LoadFile(w.path)
	if err != nil {
		return err
	}
	if err := rules.Validate(pack); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	d := detect.NewDetector(pack, w.detectorVersion)
	w.current.Store(d)
	w.rulesCount.Store(int64(len(pack)))
	if fi, err := os.Stat(w.path); err == nil {
		w.lastMtime.Store(fi.ModTime().UnixNano())
	}
	return nil
}
