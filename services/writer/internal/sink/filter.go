package sink

import "strings"

// WantedByFinding returns true if the sink should write this finding.
// When formats is empty, all enabled sinks fire (back-compat with
// callers that haven't started using per-scan format selection).
// Otherwise the sink's short name (Name() minus the "_file" suffix)
// is matched case-insensitively against the requested set.
//
// The short-name convention covers every shipping sink:
//
//	Sink.Name()    short name (matches request)
//	ndjson_file    ndjson
//	sarif_file     sarif
//	html_file      html
//	xlsx_file      xlsx
//	pdf_file       pdf
func WantedByFinding(s Sink, formats []string) bool {
	if len(formats) == 0 {
		return true
	}
	short := shortName(s.Name())
	for _, f := range formats {
		if strings.EqualFold(short, strings.TrimSpace(f)) {
			return true
		}
	}
	return false
}

func shortName(n string) string {
	return strings.TrimSuffix(n, "_file")
}
