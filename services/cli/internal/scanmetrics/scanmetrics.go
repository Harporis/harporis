// Package scanmetrics merges the partial ScanMetrics that arrive split across
// producers and events. The getter stamps throughput (blobs/chunks/bytes/
// errors) on the terminal COMPLETED event; the scanner stamps secrets_found on
// separate RUNNING events. No single status event carries the full picture, so
// any consumer that shows a per-scan summary must merge. Every counter is
// monotonic non-decreasing within a scan, so a field-wise max is the correct
// merge.
package scanmetrics

import v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"

// Merge returns a new ScanMetrics whose every field is the max of a and b.
// Either argument may be nil (treated as all-zero). Returns nil only when both
// are nil, so a scan that never reported metrics stays metric-less.
func Merge(a, b *v1.ScanMetrics) *v1.ScanMetrics {
	if a == nil && b == nil {
		return nil
	}
	return &v1.ScanMetrics{
		BlobsScanned:    maxI64(a.GetBlobsScanned(), b.GetBlobsScanned()),
		BlobsSkipped:    maxI64(a.GetBlobsSkipped(), b.GetBlobsSkipped()),
		ChunksPublished: maxI64(a.GetChunksPublished(), b.GetChunksPublished()),
		BytesPublished:  maxI64(a.GetBytesPublished(), b.GetBytesPublished()),
		ErrorsTotal:     maxI64(a.GetErrorsTotal(), b.GetErrorsTotal()),
		DurationMs:      maxI64(a.GetDurationMs(), b.GetDurationMs()),
		SecretsFound:    maxI64(a.GetSecretsFound(), b.GetSecretsFound()),
	}
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
