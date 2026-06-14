// Streaming HTML sink. Renders findings into a self-contained HTML
// report (one file per scan_id at <rootDir>/<scan_id>.html). Designed
// to be "double-click and open in browser" — no external CSS/JS,
// includes inline sort + filter so an analyst can triage findings
// without any pipeline tooling.
//
// Streaming model:
//   * On the FIRST Write for a scan_id, the sink opens a tempfile and
//     writes the HTML head + opening table + `<tbody>`.
//   * Each subsequent Write appends one `<tr>` (plus an optional
//     hidden context row) per finding. O(1) amortised per Write;
//     O(N) total bytes written per scan.
//   * Finalize closes `</tbody></table>` + the inline JS (sort,
//     filter, context-toggle, severity-count-from-DOM) + closing
//     body/html, fsyncs, and renames onto the final path.
//
// The inline JS now computes severity counts from rendered rows at
// page load instead of getting them server-side. That lets the sink
// stream rows without knowing the final tallies up front.

package sink

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
	"github.com/Harporis/harporis/services/writer/internal/metrics"
)

const htmlSinkLabel = "html_file"

// HTMLDefaultMaxPerScan caps streamed rows per scan. Past this Write
// returns an error.
const HTMLDefaultMaxPerScan = 10_000

const htmlMinIdleTimeout = 30 * time.Second

const htmlFinalizedCap = 4096

// HTML emits one streaming .html report per scan_id.
type HTML struct {
	rootDir     string
	maxPerScan  int
	idleTimeout time.Duration
	replicaID   string
	maskSecret  bool

	mu             sync.Mutex
	closed         bool
	scans          map[string]*htmlScanState
	finalized      map[string]struct{}
	finalizedOrder []string

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type htmlScanState struct {
	mu        sync.Mutex
	file      *os.File
	tmpPath   string
	finalPath string
	rowCount  int
	closed    bool
	lastWrite time.Time
}

// SetMaskSecrets toggles secret-masking in rendered cards. Off by
// default — operators reviewing locally usually want the full secret.
func (h *HTML) SetMaskSecrets(on bool) { h.maskSecret = on }

func NewHTML(rootDir string) (*HTML, error) {
	return NewHTMLN(rootDir, HTMLDefaultMaxPerScan)
}

func NewHTMLN(rootDir string, maxPerScan int) (*HTML, error) {
	return NewHTMLConfig(rootDir, BatchConfig{MaxPerScan: maxPerScan})
}

func NewHTMLConfig(rootDir string, cfg BatchConfig) (*HTML, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if cfg.MaxPerScan <= 0 {
		cfg.MaxPerScan = HTMLDefaultMaxPerScan
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	metrics.Init()
	idleTimeout := cfg.FlushInterval
	if idleTimeout > 0 && idleTimeout < htmlMinIdleTimeout {
		idleTimeout = htmlMinIdleTimeout
	}
	h := &HTML{
		rootDir:     rootDir,
		maxPerScan:  cfg.MaxPerScan,
		idleTimeout: idleTimeout,
		scans:       make(map[string]*htmlScanState),
		finalized:   make(map[string]struct{}, htmlFinalizedCap),
		stopCh:      make(chan struct{}),
	}
	if h.idleTimeout > 0 {
		h.wg.Add(1)
		go h.runIdleSweeper()
	}
	return h, nil
}

func (h *HTML) Name() string { return htmlSinkLabel }

func (h *HTML) SetReplicaID(id string) { h.replicaID = id }

func (h *HTML) Write(ctx context.Context, f *v1.Finding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	if err := kitscan.ValidateScanID(f.ScanId); err != nil {
		return fmt.Errorf("sink: %w", err)
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrSinkClosed
	}
	if _, done := h.finalized[f.ScanId]; done {
		h.mu.Unlock()
		metrics.SinkPostFinalizeDropped.WithLabelValues(htmlSinkLabel).Inc()
		return nil
	}
	st, ok := h.scans[f.ScanId]
	if !ok {
		var err error
		st, err = h.newScanState(f.ScanId)
		if err != nil {
			h.mu.Unlock()
			return err
		}
		h.scans[f.ScanId] = st
	}
	h.mu.Unlock()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return ErrSinkClosed
	}
	if st.rowCount >= h.maxPerScan {
		return fmt.Errorf("sink: scan %s exceeded max %d findings", f.ScanId, h.maxPerScan)
	}
	row := h.buildRow(f, st.rowCount)
	if _, err := st.file.Write([]byte(row)); err != nil {
		return fmt.Errorf("sink: html row: %w", err)
	}
	st.rowCount++
	st.lastWrite = time.Now()
	metrics.SinkPendingFindings.WithLabelValues(htmlSinkLabel).Inc()
	return nil
}

func (h *HTML) Finalize(_ context.Context, scanID string) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrSinkClosed
	}
	st, ok := h.scans[scanID]
	if !ok {
		h.mu.Unlock()
		return nil
	}
	delete(h.scans, scanID)
	h.markFinalizedLocked(scanID)
	h.mu.Unlock()
	return st.closeAndRename("terminal")
}

func (h *HTML) markFinalizedLocked(scanID string) {
	if _, exists := h.finalized[scanID]; exists {
		return
	}
	h.finalized[scanID] = struct{}{}
	h.finalizedOrder = append(h.finalizedOrder, scanID)
	for len(h.finalizedOrder) > htmlFinalizedCap {
		evict := h.finalizedOrder[0]
		h.finalizedOrder = h.finalizedOrder[1:]
		delete(h.finalized, evict)
	}
}

func (h *HTML) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	states := make([]*htmlScanState, 0, len(h.scans))
	for _, st := range h.scans {
		states = append(states, st)
	}
	h.scans = nil
	h.mu.Unlock()

	close(h.stopCh)
	h.wg.Wait()

	var firstErr error
	for _, st := range states {
		if err := st.closeAndRename("close"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h *HTML) Flush() error { return nil }

func (h *HTML) runIdleSweeper() {
	defer h.wg.Done()
	tickEvery := h.idleTimeout / 2
	if tickEvery < time.Second {
		tickEvery = time.Second
	}
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-h.stopCh:
			return
		case <-t.C:
			h.finalizeIdleLocked()
		}
	}
}

func (h *HTML) finalizeIdleLocked() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	now := time.Now()
	var toFinalize []*htmlScanState
	for id, st := range h.scans {
		st.mu.Lock()
		idle := !st.lastWrite.IsZero() && now.Sub(st.lastWrite) >= h.idleTimeout
		st.mu.Unlock()
		if idle {
			toFinalize = append(toFinalize, st)
			delete(h.scans, id)
			h.markFinalizedLocked(id)
		}
	}
	h.mu.Unlock()

	for _, st := range toFinalize {
		_ = st.closeAndRename("idle")
	}
}

func (h *HTML) newScanState(scanID string) (*htmlScanState, error) {
	finalName := scanID + ".html"
	if h.replicaID != "" {
		finalName = scanID + "." + h.replicaID + ".html"
	}
	finalPath := filepath.Join(h.rootDir, finalName)
	rootClean := filepath.Clean(h.rootDir)
	if !strings.HasPrefix(filepath.Clean(finalPath), rootClean+string(filepath.Separator)) {
		return nil, fmt.Errorf("sink: path %q escapes rootDir %q", finalPath, h.rootDir)
	}
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	tmpPath := filepath.Join(h.rootDir, fmt.Sprintf(".%s.%s.html", scanID, hex.EncodeToString(nonce[:])))
	file, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("sink: html tempfile: %w", err)
	}
	var prefix bytes.Buffer
	if err := htmlPrefixTemplate.Execute(&prefix, struct{ ScanID string }{ScanID: scanID}); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("sink: html prefix: %w", err)
	}
	if _, err := file.Write(prefix.Bytes()); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("sink: html prefix write: %w", err)
	}
	return &htmlScanState{
		file:      file,
		tmpPath:   tmpPath,
		finalPath: finalPath,
	}, nil
}

type htmlCtxLine struct {
	LineNo  int32
	Content string
	Matched bool
}

type htmlRow struct {
	Index    int
	Severity string
	RuleID   string
	Path     string
	Line     int32
	Secret   string
	Context  []htmlCtxLine
}

// buildRow renders one `<tr>` (plus an optional context row) for f.
// Uses html/template to escape the dynamic fields.
func (h *HTML) buildRow(f *v1.Finding, index int) string {
	p := f.FilePath
	if p == "" && len(f.Refs) > 0 {
		p = f.Refs[0].Path
	}
	var ctx []htmlCtxLine
	if len(f.ContextBefore) > 0 || len(f.ContextAfter) > 0 {
		ctx = make([]htmlCtxLine, 0, len(f.ContextBefore)+1+len(f.ContextAfter))
		startLine := f.LineNumber - int32(len(f.ContextBefore))
		if startLine < 1 {
			startLine = 1
		}
		ln := startLine
		for _, b := range f.ContextBefore {
			ctx = append(ctx, htmlCtxLine{LineNo: ln, Content: string(b)})
			ln++
		}
		ctx = append(ctx, htmlCtxLine{LineNo: f.LineNumber, Content: string(f.MatchedLine), Matched: true})
		ln = f.LineNumberEnd + 1
		if ln <= 0 {
			ln = f.LineNumber + 1
		}
		for _, a := range f.ContextAfter {
			ctx = append(ctx, htmlCtxLine{LineNo: ln, Content: string(a)})
			ln++
		}
	}
	secret := string(f.MatchedSecret)
	if h.maskSecret {
		secret = maskSecret(secret)
	}
	r := htmlRow{
		Index:    index,
		Severity: f.Severity.String(),
		RuleID:   f.RuleId,
		Path:     p,
		Line:     f.LineNumber,
		Secret:   secret,
		Context:  ctx,
	}
	var buf bytes.Buffer
	if err := htmlRowTemplate.Execute(&buf, r); err != nil {
		// Template execution failures shouldn't happen in practice; if
		// they do, emit a visible "render error" cell rather than
		// silently dropping the row.
		return fmt.Sprintf("<tr><td colspan=5>html render error: %v</td></tr>\n", err)
	}
	return buf.String()
}

func (st *htmlScanState) closeAndRename(trigger string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil
	}
	st.closed = true
	rowCount := st.rowCount
	defer func() {
		if rowCount > 0 {
			metrics.SinkPendingFindings.WithLabelValues(htmlSinkLabel).Sub(float64(rowCount))
		}
		metrics.SinkFlushTotal.WithLabelValues(htmlSinkLabel, trigger).Inc()
	}()
	cleanup := func() {
		_ = st.file.Close()
		_ = os.Remove(st.tmpPath)
	}
	if _, err := st.file.Write([]byte(htmlSuffix)); err != nil {
		cleanup()
		return fmt.Errorf("sink: html suffix: %w", err)
	}
	if err := st.file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sink: html sync: %w", err)
	}
	if err := st.file.Close(); err != nil {
		_ = os.Remove(st.tmpPath)
		return fmt.Errorf("sink: html close: %w", err)
	}
	if err := os.Rename(st.tmpPath, st.finalPath); err != nil {
		_ = os.Remove(st.tmpPath)
		return fmt.Errorf("sink: html rename to %s: %w", st.finalPath, err)
	}
	return nil
}

// htmlPrefixTemplate is the static head + style + filter input + table
// opener. The closing JS computes severity counts from the rendered
// tbody on page load, so the prefix doesn't need to know totals.
var htmlPrefixTemplate = template.Must(template.New("htmlPrefix").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Harporis: {{.ScanID}}</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem; color: #1a1a1a; }
  h1 { font-size: 1.2rem; margin-bottom: 0.2rem; }
  .meta { color: #666; font-size: 0.85rem; margin-bottom: 1rem; }
  .counts { display: flex; gap: 0.5rem; margin-bottom: 1rem; flex-wrap: wrap; }
  .badge { padding: 0.2rem 0.6rem; border-radius: 999px; font-size: 0.8rem; font-weight: 600; }
  .CRITICAL { background: #fde8e8; color: #9b1c1c; }
  .HIGH { background: #fef3c7; color: #92400e; }
  .MEDIUM { background: #fffbeb; color: #b45309; }
  .LOW { background: #e0f2fe; color: #0369a1; }
  .SEVERITY_UNSPECIFIED { background: #e5e7eb; color: #374151; }
  input[type="search"] { padding: 0.4rem 0.6rem; border: 1px solid #d4d4d4; border-radius: 4px; font-size: 0.9rem; width: 320px; margin-bottom: 0.8rem; }
  table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  th, td { padding: 0.5rem 0.6rem; border-bottom: 1px solid #eee; text-align: left; vertical-align: top; }
  th { background: #f7f7f7; cursor: pointer; user-select: none; position: sticky; top: 0; }
  th:hover { background: #efefef; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: #f1f1f1; padding: 1px 4px; border-radius: 3px; word-break: break-all; }
  .secret { max-width: 420px; overflow: hidden; text-overflow: ellipsis; }
  .ctx-toggle { background: none; border: none; color: #2563eb; cursor: pointer; font-size: 0.8rem; padding: 0; }
  .ctx-toggle:hover { text-decoration: underline; }
  .ctx-row { background: #fafafa; }
  .ctx-row td { padding: 0; }
  .ctx-row pre { margin: 0; padding: 0.6rem 0.8rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.78rem; white-space: pre; overflow-x: auto; color: #444; }
  .ctx-row .ln { color: #999; user-select: none; display: inline-block; width: 3.5em; text-align: right; padding-right: 0.6em; }
  .ctx-row .match { background: #fef9c3; display: block; }
</style>
</head>
<body>
<h1>Harporis findings — <code>{{.ScanID}}</code></h1>
<div class="meta"><span id="totalCount">0</span> finding(s) total</div>
<div class="counts" id="counts"></div>
<input type="search" id="q" placeholder="Filter by rule, path, secret…">
<table id="t">
<thead><tr>
  <th data-key="sev">Severity</th>
  <th data-key="rule">Rule</th>
  <th data-key="path">Path</th>
  <th data-key="line">Line</th>
  <th>Secret</th>
</tr></thead>
<tbody>
`))

// htmlRowTemplate is one `<tr>` (and an optional adjacent context row).
var htmlRowTemplate = template.Must(template.New("htmlRow").Parse(`<tr data-sev="{{.Severity}}">
  <td><span class="badge {{.Severity}}">{{.Severity}}</span></td>
  <td><code>{{.RuleID}}</code></td>
  <td><code>{{.Path}}</code></td>
  <td>{{.Line}}</td>
  <td class="secret"><code>{{.Secret}}</code>{{if .Context}} <button class="ctx-toggle" data-target="ctx-{{.Index}}">show context</button>{{end}}</td>
</tr>
{{- if .Context}}
<tr class="ctx-row" id="ctx-{{.Index}}" style="display:none"><td colspan="5"><pre>{{range .Context}}<span{{if .Matched}} class="match"{{end}}><span class="ln">{{.LineNo}}</span>{{.Content}}
</span>{{end}}</pre></td></tr>
{{- end}}
`))

// htmlSuffix closes the table, embeds the sort/filter/counts script,
// then closes body+html. Built from string concatenation rather than
// template.Execute — there are no dynamic fields here.
const htmlSuffix = `</tbody>
</table>
<script>
  const sevOrder = {CRITICAL: 0, HIGH: 1, MEDIUM: 2, LOW: 3, SEVERITY_UNSPECIFIED: 4};

  // Severity counts: walk rendered finding rows and group by data-sev.
  // Done at load time so the streaming sink doesn't have to know totals.
  (function renderCounts() {
    const counts = {};
    document.querySelectorAll("#t tbody tr[data-sev]").forEach(r => {
      const k = r.dataset.sev;
      counts[k] = (counts[k] || 0) + 1;
    });
    const target = document.getElementById("counts");
    Object.keys(counts).sort((a, b) => (sevOrder[a] ?? 99) - (sevOrder[b] ?? 99)).forEach(k => {
      const s = document.createElement("span");
      s.className = "badge " + k;
      s.textContent = k + ": " + counts[k];
      target.appendChild(s);
    });
    document.getElementById("totalCount").textContent =
      Object.values(counts).reduce((a, b) => a + b, 0);
  })();

  document.querySelectorAll("th[data-key]").forEach((th, i) => {
    th.addEventListener("click", () => {
      const tbody = document.querySelector("#t tbody");
      const findingRows = Array.from(tbody.querySelectorAll("tr[data-sev]"));
      const dir = th.dataset.dir === "asc" ? -1 : 1;
      th.dataset.dir = dir === 1 ? "asc" : "desc";
      // Sort finding rows, then re-append each (with its trailing
      // context row if present) to preserve pairing.
      findingRows.sort((a, b) => {
        const av = a.cells[i].innerText.trim();
        const bv = b.cells[i].innerText.trim();
        if (th.dataset.key === "sev") {
          return dir * ((sevOrder[av] ?? 99) - (sevOrder[bv] ?? 99));
        }
        if (th.dataset.key === "line") {
          return dir * (parseInt(av || "0") - parseInt(bv || "0"));
        }
        return dir * av.localeCompare(bv);
      });
      findingRows.forEach(r => {
        tbody.appendChild(r);
        const nextSib = r.nextElementSibling;
        if (nextSib && nextSib.classList.contains("ctx-row")) {
          tbody.appendChild(nextSib);
        }
      });
    });
  });

  document.getElementById("q").addEventListener("input", e => {
    const q = e.target.value.toLowerCase();
    document.querySelectorAll("#t tbody tr").forEach(row => {
      if (row.classList.contains("ctx-row")) return;
      row.style.display = row.innerText.toLowerCase().includes(q) ? "" : "none";
    });
  });

  document.querySelectorAll(".ctx-toggle").forEach(btn => {
    btn.addEventListener("click", () => {
      const tr = document.getElementById(btn.dataset.target);
      if (!tr) return;
      const open = tr.style.display !== "none";
      tr.style.display = open ? "none" : "table-row";
      btn.textContent = open ? "show context" : "hide context";
    });
  });
</script>
</body>
</html>
`
