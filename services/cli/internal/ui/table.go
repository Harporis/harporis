package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Table is a minimal column writer. We don't use lipgloss tables — these
// rows are short and we want pipe-friendly output.
type Table struct {
	headers []string
	rows    [][]string
}

// NewTable creates a table with the given column headers.
func NewTable(headers ...string) *Table { return &Table{headers: headers} }

// Row adds a row. Excess columns are dropped; missing ones get "".
func (t *Table) Row(cols ...string) {
	row := make([]string, len(t.headers))
	for i := range row {
		if i < len(cols) {
			row[i] = cols[i]
		}
	}
	t.rows = append(t.rows, row)
}

// WriteTo renders the table. Column widths are measured with
// lipgloss.Width so that ANSI-styled cells (e.g. a colored STATE column)
// align by their VISIBLE width, not their byte length.
func (t *Table) WriteTo(w io.Writer) (int64, error) {
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = lipgloss.Width(h)
	}
	for _, r := range t.rows {
		for i, c := range r {
			if cw := lipgloss.Width(c); cw > widths[i] {
				widths[i] = cw
			}
		}
	}
	var b strings.Builder
	for i, h := range t.headers {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(DimStyle.Render(padRight(h, widths[i])))
	}
	b.WriteString("\n")
	for _, r := range t.rows {
		for i, c := range r {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(padRight(c, widths[i]))
		}
		b.WriteString("\n")
	}
	n, err := fmt.Fprint(w, b.String())
	return int64(n), err
}

// padRight pads s with trailing spaces to a VISIBLE width of n, ignoring
// any ANSI escape sequences embedded in s.
func padRight(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}
