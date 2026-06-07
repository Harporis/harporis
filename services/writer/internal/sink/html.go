// HTML sink. Renders findings into a self-contained HTML report (one
// file per scan_id at <rootDir>/<scan_id>.html). Designed to be
// "double-click and open in browser" — no external CSS/JS, includes
// inline sort + filter so an analyst can triage findings without
// any pipeline tooling.
//
// Memory model mirrors SARIF: keep an in-memory accumulator per
// scan_id, capped at maxPerScan, and rewrite the file atomically
// on every Write so a partial scan is always inspectable.
package sink

import (
	"context"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"sync"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
)

// HTMLDefaultMaxPerScan matches SARIF's cap — keeps memory bounded
// under runaway producers.
const HTMLDefaultMaxPerScan = 10_000

// HTML emits one HTML report per scan_id to rootDir.
type HTML struct {
	rootDir    string
	maxPerScan int

	mu     sync.Mutex
	closed bool
	scans  map[string][]*v1.Finding
}

func NewHTML(rootDir string) (*HTML, error) {
	return NewHTMLN(rootDir, HTMLDefaultMaxPerScan)
}

func NewHTMLN(rootDir string, maxPerScan int) (*HTML, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if maxPerScan <= 0 {
		maxPerScan = HTMLDefaultMaxPerScan
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	return &HTML{
		rootDir:    rootDir,
		maxPerScan: maxPerScan,
		scans:      make(map[string][]*v1.Finding),
	}, nil
}

func (h *HTML) Name() string { return "html_file" }

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
	findings := append(h.scans[f.ScanId], f)
	if len(findings) > h.maxPerScan {
		h.mu.Unlock()
		return fmt.Errorf("sink: scan %s exceeded max %d findings", f.ScanId, h.maxPerScan)
	}
	h.scans[f.ScanId] = findings
	snapshot := make([]*v1.Finding, len(findings))
	copy(snapshot, findings)
	h.mu.Unlock()
	return h.flush(f.ScanId, snapshot)
}

func (h *HTML) Close() error {
	h.mu.Lock()
	h.closed = true
	h.scans = nil
	h.mu.Unlock()
	return nil
}

func (h *HTML) flush(scanID string, findings []*v1.Finding) error {
	path := filepath.Join(h.rootDir, scanID+".html")
	rootClean := filepath.Clean(h.rootDir)
	if !strings.HasPrefix(filepath.Clean(path), rootClean+string(filepath.Separator)) {
		return fmt.Errorf("sink: path %q escapes rootDir %q", path, h.rootDir)
	}
	type ctxLine struct {
		LineNo  int32
		Content string
		Matched bool
	}
	type row struct {
		Severity string
		RuleID   string
		Path     string
		Line     int32
		Secret   string
		Context  []ctxLine // empty when scan didn't request --context > 0
	}
	rows := make([]row, 0, len(findings))
	counts := map[string]int{}
	for _, f := range findings {
		p := f.FilePath
		if p == "" && len(f.Refs) > 0 {
			p = f.Refs[0].Path
		}
		var ctx []ctxLine
		if len(f.ContextBefore) > 0 || len(f.ContextAfter) > 0 {
			ctx = make([]ctxLine, 0, len(f.ContextBefore)+1+len(f.ContextAfter))
			startLine := f.LineNumber - int32(len(f.ContextBefore))
			if startLine < 1 {
				startLine = 1
			}
			ln := startLine
			for _, b := range f.ContextBefore {
				ctx = append(ctx, ctxLine{LineNo: ln, Content: string(b)})
				ln++
			}
			ctx = append(ctx, ctxLine{LineNo: f.LineNumber, Content: string(f.MatchedLine), Matched: true})
			ln = f.LineNumberEnd + 1
			if ln <= 0 {
				ln = f.LineNumber + 1
			}
			for _, a := range f.ContextAfter {
				ctx = append(ctx, ctxLine{LineNo: ln, Content: string(a)})
				ln++
			}
		}
		rows = append(rows, row{
			Severity: f.Severity.String(),
			RuleID:   f.RuleId,
			Path:     p,
			Line:     f.LineNumber,
			Secret:   string(f.MatchedSecret),
			Context:  ctx,
		})
		counts[f.Severity.String()]++
	}
	data := struct {
		ScanID   string
		Findings []row
		Counts   map[string]int
	}{ScanID: scanID, Findings: rows, Counts: counts}

	tmp, err := os.CreateTemp(h.rootDir, scanID+".html.tmp-*")
	if err != nil {
		return fmt.Errorf("sink: tempfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := htmlTemplate.Execute(tmp, data); err != nil {
		cleanup()
		return fmt.Errorf("sink: render html: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sink: sync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("sink: close tempfile: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("sink: rename to %s: %w", path, err)
	}
	return nil
}

// htmlTemplate is the report skeleton. Inline CSS + JS so the file is
// fully self-contained — drop on a USB stick, open offline.
var htmlTemplate = template.Must(template.New("report").Parse(`<!DOCTYPE html>
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
<div class="meta">{{len .Findings}} finding(s) total</div>
<div class="counts">
{{range $sev, $n := .Counts}}<span class="badge {{$sev}}">{{$sev}}: {{$n}}</span>{{end}}
</div>
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
{{range $i, $f := .Findings}}<tr>
  <td><span class="badge {{.Severity}}">{{.Severity}}</span></td>
  <td><code>{{.RuleID}}</code></td>
  <td><code>{{.Path}}</code></td>
  <td>{{.Line}}</td>
  <td class="secret"><code>{{.Secret}}</code>{{if .Context}} <button class="ctx-toggle" data-target="ctx-{{$i}}">show context</button>{{end}}</td>
</tr>
{{if .Context}}<tr class="ctx-row" id="ctx-{{$i}}" style="display:none"><td colspan="5"><pre>{{range .Context}}<span{{if .Matched}} class="match"{{end}}><span class="ln">{{.LineNo}}</span>{{.Content}}
</span>{{end}}</pre></td></tr>
{{end}}{{end}}</tbody>
</table>
<script>
  // Sort by column on header click.
  const sevOrder = {CRITICAL: 0, HIGH: 1, MEDIUM: 2, LOW: 3, SEVERITY_UNSPECIFIED: 4};
  document.querySelectorAll("th[data-key]").forEach((th, i) => {
    th.addEventListener("click", () => {
      const tbody = document.querySelector("#t tbody");
      const rows = Array.from(tbody.querySelectorAll("tr"));
      const dir = th.dataset.dir === "asc" ? -1 : 1;
      th.dataset.dir = dir === 1 ? "asc" : "desc";
      rows.sort((a, b) => {
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
      rows.forEach(r => tbody.appendChild(r));
    });
  });
  // Live filter (skip the context-only rows; their visibility is tied
  // to the parent finding row via the data-target toggle).
  document.getElementById("q").addEventListener("input", e => {
    const q = e.target.value.toLowerCase();
    document.querySelectorAll("#t tbody tr").forEach(row => {
      if (row.classList.contains("ctx-row")) return;
      row.style.display = row.innerText.toLowerCase().includes(q) ? "" : "none";
    });
  });
  // Toggle context rows.
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
`))
