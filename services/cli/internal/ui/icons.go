package ui

// Icons is the per-render icon set. Switched between Unicode and ASCII
// based on the active output profile.
type Icons struct {
	OK     string
	Fail   string
	Run    string
	Shield string
	Step   string
	Bullet string
	Rule   string
}

// NewIcons returns the appropriate set. asciiOnly=true is for dumb
// terminals (NO_COLOR or termenv.Ascii profile).
func NewIcons(asciiOnly bool) Icons {
	if asciiOnly {
		return Icons{
			OK:     "[+]",
			Fail:   "[-]",
			Run:    "[*]",
			Shield: "[#]",
			Step:   "->",
			Bullet: "o",
			Rule:   "-",
		}
	}
	return Icons{
		OK:     "✓",
		Fail:   "✗",
		Run:    "⚡",
		Shield: "🛡",
		Step:   "▸",
		Bullet: "●",
		Rule:   "─",
	}
}
