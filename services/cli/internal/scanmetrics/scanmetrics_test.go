package scanmetrics

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestMergeBothNilReturnsNil(t *testing.T) {
	if Merge(nil, nil) != nil {
		t.Fatal("Merge(nil,nil) must be nil")
	}
}

func TestMergeNilArgTreatedAsZero(t *testing.T) {
	got := Merge(nil, &v1.ScanMetrics{SecretsFound: 5})
	if got.GetSecretsFound() != 5 {
		t.Fatalf("want 5, got %d", got.GetSecretsFound())
	}
}

func TestMergeFieldWiseMax(t *testing.T) {
	// Simulates the real split: a = getter COMPLETED (throughput, secrets 0),
	// b = scanner RUNNING (secrets only).
	a := &v1.ScanMetrics{BlobsScanned: 285, ChunksPublished: 284, BytesPublished: 1526734, SecretsFound: 0}
	b := &v1.ScanMetrics{SecretsFound: 146}
	got := Merge(a, b)
	if got.GetChunksPublished() != 284 {
		t.Fatalf("chunks: want 284, got %d", got.GetChunksPublished())
	}
	if got.GetBytesPublished() != 1526734 {
		t.Fatalf("bytes: want 1526734, got %d", got.GetBytesPublished())
	}
	if got.GetSecretsFound() != 146 {
		t.Fatalf("secrets: want 146, got %d", got.GetSecretsFound())
	}
	// Symmetric.
	got2 := Merge(b, a)
	if got2.GetSecretsFound() != 146 || got2.GetChunksPublished() != 284 {
		t.Fatalf("merge must be symmetric, got secrets=%d chunks=%d", got2.GetSecretsFound(), got2.GetChunksPublished())
	}
}
