package doctor

import "testing"

func TestRunAllCollectsResults(t *testing.T) {
	checks := []Check{
		StaticCheck("always-ok", true, "no detail"),
		StaticCheck("always-bad", false, "broken"),
	}
	results := RunAll(checks)
	if len(results) != 2 {
		t.Fatalf("got %d", len(results))
	}
	if !results[0].OK || results[1].OK {
		t.Fatalf("unexpected: %+v", results)
	}
	if results[0].Detail != "no detail" || results[1].Detail != "broken" {
		t.Fatalf("detail not propagated: %+v", results)
	}
}
