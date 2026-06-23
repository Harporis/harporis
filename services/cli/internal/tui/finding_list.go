package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Harporis/harporis/services/cli/internal/findings"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

// findingsState backs the Findings tab of the detail panel.
type findingsState struct {
	loaded       []findings.Finding
	loading      bool
	loadedOnce   bool
	err          error
	cursor       int
	offset       int
	sortCol      findingColumn
	sortRev      bool
	sortExplicit bool
	filter       FindingFilter
	filtering    bool
	filterInput  string
	filterErr    string
}

// visible returns the filtered + sorted findings. Default (non-explicit)
// order is severity desc, tiebreak path:line; once a column is chosen it
// sorts purely by that column with a Location tiebreak.
func (s findingsState) visible() []findings.Finding {
	out := make([]findings.Finding, 0, len(s.loaded))
	for _, f := range s.loaded {
		if s.filter.Match(f) {
			out = append(out, f)
		}
	}
	if !s.sortExplicit {
		sort.SliceStable(out, func(i, j int) bool {
			ri, rj := findings.SeverityRank(out[i].Severity), findings.SeverityRank(out[j].Severity)
			if ri != rj {
				return ri > rj // desc
			}
			return out[i].Location() < out[j].Location()
		})
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		c := compareFinding(out[i], out[j], s.sortCol)
		if c == 0 {
			return out[i].Location() < out[j].Location()
		}
		if s.sortRev {
			return c > 0
		}
		return c < 0
	})
	return out
}

func (s *findingsState) clampCursor() {
	n := len(s.visible())
	if n == 0 {
		s.cursor = 0
		s.offset = 0
	} else if s.cursor >= n {
		s.cursor = n - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// Cursor exposes the selected row for tests.
func (s findingsState) Cursor() int { return s.cursor }

// updateKey handles findings-internal keys. It returns the new state and
// back=true when esc/q should bubble up to the detail model (back to fleet).
func (s findingsState) updateKey(v tea.KeyMsg, height int) (findingsState, bool) {
	if s.filtering {
		switch v.String() {
		case "esc":
			s.filtering = false
			s.filterErr = ""
		case "enter":
			f, err := ParseFindingFilter(s.filterInput)
			if err != nil {
				s.filterErr = "filter error: " + err.Error()
			} else {
				s.filter = f
				s.filtering = false
				s.filterErr = ""
				s.clampCursor()
			}
		case "backspace":
			if r := []rune(s.filterInput); len(r) > 0 {
				s.filterInput = string(r[:len(r)-1])
			}
		default:
			if len(v.Runes) > 0 {
				s.filterInput += string(v.Runes)
			}
		}
		return s, false
	}
	rows := s.visible()
	switch v.String() {
	case "esc", "q":
		return s, true
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < len(rows)-1 {
			s.cursor++
		}
	case "s":
		if !s.sortExplicit {
			s.sortExplicit = true
			s.sortCol = fcolSeverity
		} else {
			s.sortCol = s.sortCol.next()
		}
		s.clampCursor()
	case "S":
		if !s.sortExplicit {
			s.sortExplicit = true
			s.sortCol = fcolSeverity
		}
		s.sortRev = !s.sortRev
	case "/":
		s.filtering = true
		s.filterInput = s.filter.Raw()
		s.filterErr = ""
	}
	ps := s.pageSize(height)
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if ps > 0 && s.cursor >= s.offset+ps {
		s.offset = s.cursor - ps + 1
	}
	if s.offset < 0 {
		s.offset = 0
	}
	return s, false
}

func (s findingsState) columnHeader(c findingColumn) string {
	h := c.label()
	if s.sortExplicit && s.sortCol == c {
		if s.sortRev {
			return h + "↓"
		}
		return h + "↑"
	}
	return h
}

func (s findingsState) pageSize(height int) int {
	if height > 12 {
		return height - 10
	}
	return 10
}

// view renders the Findings tab body.
func (s findingsState) view(height int) string {
	switch {
	case s.loading:
		return ui.DimStyle.Render("  loading findings…")
	case s.err != nil:
		return ui.ErrStyle.Render("  findings unavailable: "+s.err.Error()) + "\n" +
			ui.DimStyle.Render("  press r to retry")
	case len(s.loaded) == 0:
		return ui.DimStyle.Render("  (no findings)")
	}
	rows := s.visible()
	if len(rows) == 0 {
		return ui.DimStyle.Render("  (no findings match)") + "\n" +
			s.filterLine()
	}
	t := ui.NewTable("", s.columnHeader(fcolSeverity), s.columnHeader(fcolRule),
		s.columnHeader(fcolPath), s.columnHeader(fcolSecret))
	start := s.offset
	if start > len(rows) {
		start = len(rows)
	}
	end := start + s.pageSize(height)
	if end > len(rows) {
		end = len(rows)
	}
	for i := start; i < end; i++ {
		f := rows[i]
		marker := " "
		if i == s.cursor {
			marker = ui.BrandStyle.Render(">")
		}
		rule := f.RuleID
		if rule == "" {
			rule = "-"
		}
		t.Row(marker, ui.SeverityStyle(f.Severity).Render(f.Severity), rule, f.Location(), f.SecretPreview(48))
	}
	var sb strings.Builder
	_, _ = t.WriteTo(&sb)
	if fl := s.filterLine(); fl != "" {
		sb.WriteString(fl)
	}
	return sb.String()
}

func (s findingsState) filterLine() string {
	var sb strings.Builder
	if s.filtering {
		sb.WriteString(ui.InfoStyle.Render("filter> "+s.filterInput) + "\n")
	} else if s.filter.Raw() != "" {
		sb.WriteString(ui.DimStyle.Render("filter: "+s.filter.Raw()) + "\n")
	}
	if s.filterErr != "" {
		sb.WriteString(ui.ErrStyle.Render(s.filterErr) + "\n")
	}
	return sb.String()
}
