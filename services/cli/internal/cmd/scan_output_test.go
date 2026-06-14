package cmd

import (
	"reflect"
	"testing"
)

func TestFilterFindingsForScan(t *testing.T) {
	ls := `aff-a.html
aff-a.ndjson
aff-a.parquet
aff-a.pdf
aff-a.sarif
aff-a.xlsx
aff-b.ndjson
aff-b.parquet
unrelated.txt
.hidden.aff-a.xlsx
aff-a.sarif.tmp-12345
aff-a.replica1.pdf
`
	got := filterFindingsForScan(ls, "aff-a")
	want := []string{
		"aff-a.html",
		"aff-a.ndjson",
		"aff-a.parquet",
		"aff-a.pdf",
		"aff-a.replica1.pdf",
		"aff-a.sarif",
		"aff-a.xlsx",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterFindingsForScan(aff-a) =\n  %#v\nwant\n  %#v", got, want)
	}
}

func TestFilterFindingsForScan_NoMatches(t *testing.T) {
	ls := `something.txt
other.ndjson`
	got := filterFindingsForScan(ls, "missing-scan")
	if len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}
}

func TestFilterFindingsForScan_DoesNotMatchPrefix(t *testing.T) {
	// aff-aaa must not match scan_id "aff-a" (prefix collision).
	ls := `aff-aaa.ndjson
aff-aaab.parquet
aff-a.ndjson`
	got := filterFindingsForScan(ls, "aff-a")
	want := []string{"aff-a.ndjson"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterFindingsForScan = %v, want %v", got, want)
	}
}
