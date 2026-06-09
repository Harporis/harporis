package rules

import "math"

// ShannonEntropy returns the Shannon entropy (bits per byte) of the input.
// 0 for empty / single-symbol inputs; ~8 for cryptographically random bytes.
// Used by the detector to filter out low-entropy false positives that match
// a regex but are obviously not secrets (e.g. "tokentokentoken").
func ShannonEntropy(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	var counts [256]int
	for _, c := range b {
		counts[c]++
	}
	n := float64(len(b))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}
