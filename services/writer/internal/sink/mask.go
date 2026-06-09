package sink

// maskSecret renders a secret as its first 4 characters plus "***" so
// PDF/HTML reports can leave the security team without leaking the
// full value. Short secrets (<=4 chars) collapse to "***" — printing
// even a 3-char prefix of a 4-char secret gives away ~75% of it.
//
// NDJSON / SARIF / XLSX sinks do NOT apply masking because their
// primary consumers (jq pipelines, code-scanning ingestion, in-team
// spreadsheet triage) need the raw secret. The operator opts into
// masking the human-facing PDF+HTML reports via writer.yaml
// `mask_secrets: true`.
func maskSecret(s string) string {
	if len(s) <= 4 {
		return "***"
	}
	return s[:4] + "***"
}
