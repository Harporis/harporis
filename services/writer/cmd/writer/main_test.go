package main

import "testing"

func TestParseReplicaIndexFromHostname(t *testing.T) {
	cases := []struct {
		hostname   string
		shardCount int
		wantIdx    int
		wantOK     bool
	}{
		{"harporis-writer-1", 4, 0, true},
		{"harporis-writer-3", 4, 2, true},
		{"some-project-writer-2", 8, 1, true},
		// Out of range.
		{"harporis-writer-5", 4, 0, false},
		// No trailing number.
		{"harporis-writer", 4, 0, false},
		// Number without preceding dash.
		{"harporiswriter1", 4, 0, false},
		// Empty.
		{"", 4, 0, false},
		// Zero-indexed compose would round to -1 which we reject.
		{"writer-0", 4, 0, false},
	}
	for _, c := range cases {
		got, ok := parseReplicaIndexFromHostname(c.hostname, c.shardCount)
		if ok != c.wantOK || got != c.wantIdx {
			t.Errorf("parseReplicaIndexFromHostname(%q, %d) = (%d, %v), want (%d, %v)",
				c.hostname, c.shardCount, got, ok, c.wantIdx, c.wantOK)
		}
	}
}
