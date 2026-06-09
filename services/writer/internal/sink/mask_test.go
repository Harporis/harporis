package sink

import "testing"

func TestMaskSecret(t *testing.T) {
	cases := map[string]string{
		"":                  "***",
		"abc":               "***",
		"abcd":              "***", // short collapse — no prefix leak
		"abcde":             "abcd***",
		"AKIAIOSFODNN7EXAMPLE": "AKIA***",
	}
	for in, want := range cases {
		if got := maskSecret(in); got != want {
			t.Errorf("maskSecret(%q) = %q, want %q", in, got, want)
		}
	}
}
