package nats

import "testing"

func TestMsgIDStable(t *testing.T) {
	a := FindingMsgID("scan-1", "chunk-1", "aws-access-key-id", 42, 7)
	b := FindingMsgID("scan-1", "chunk-1", "aws-access-key-id", 42, 7)
	if a != b {
		t.Errorf("MsgID unstable: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Errorf("MsgID len = %d, want 64 (hex sha256)", len(a))
	}
}

func TestMsgIDChangesPerDimension(t *testing.T) {
	base := FindingMsgID("scan-1", "chunk-1", "rule", 1, 0)
	cases := map[string]string{
		"scan_id":    FindingMsgID("scan-2", "chunk-1", "rule", 1, 0),
		"chunk_id":   FindingMsgID("scan-1", "chunk-2", "rule", 1, 0),
		"rule_id":    FindingMsgID("scan-1", "chunk-1", "other", 1, 0),
		"line":       FindingMsgID("scan-1", "chunk-1", "rule", 2, 0),
		"byteOffset": FindingMsgID("scan-1", "chunk-1", "rule", 1, 8),
	}
	for name, got := range cases {
		if got == base {
			t.Errorf("changing %s did not change MsgID", name)
		}
	}
}
