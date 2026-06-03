package worker

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/scanner/internal/detect"
	"github.com/Harporis/harporis/services/scanner/internal/rules"
)

type fakePublisher struct {
	mu       sync.Mutex
	findings []*v1.Finding
}

func (p *fakePublisher) PublishFinding(_ context.Context, f *v1.Finding) error {
	p.mu.Lock()
	p.findings = append(p.findings, f)
	p.mu.Unlock()
	return nil
}

type fakeTracker struct {
	mu     sync.Mutex
	incs   map[string]int64
	final  []string
}

func (t *fakeTracker) Incr(scanID string, n int64) {
	t.mu.Lock()
	if t.incs == nil {
		t.incs = map[string]int64{}
	}
	t.incs[scanID] += n
	t.mu.Unlock()
}
func (t *fakeTracker) FinalEmit(_ context.Context, scanID string) error {
	t.mu.Lock()
	t.final = append(t.final, scanID)
	t.mu.Unlock()
	return nil
}

func TestProcessChunk_EmitsFindingsAndIncrsCounter(t *testing.T) {
	r := rules.Rule{
		ID: "test", Severity: v1.Severity_HIGH,
		Regex: regexp.MustCompile(`AKIA[A-Z0-9]{16}`),
	}
	d := detect.NewDetector([]rules.Rule{r}, "scanner/test")
	pub := &fakePublisher{}
	tr := &fakeTracker{}

	h := NewHandler(d, pub, tr)
	c := &v1.GitRowChunk{
		ScanId: "scan-1", ChunkId: "c-1", Kind: v1.ChunkKind_BLOB,
		Rows: []*v1.GitRow{
			{LineNumber: 1, Content: []byte("AKIAIOSFODNN7EXAMPLE")},
		},
	}
	if err := h.Handle(context.Background(), c); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(pub.findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(pub.findings))
	}
	tr.mu.Lock()
	if tr.incs["scan-1"] != 1 {
		t.Errorf("Incr count = %d, want 1", tr.incs["scan-1"])
	}
	tr.mu.Unlock()
}

func TestProcessChunk_TriggersFinalEmitOnIsLast(t *testing.T) {
	r := rules.Rule{ID: "test", Severity: v1.Severity_HIGH, Regex: regexp.MustCompile(`nope`)}
	d := detect.NewDetector([]rules.Rule{r}, "scanner/test")
	pub := &fakePublisher{}
	tr := &fakeTracker{}

	h := NewHandler(d, pub, tr)
	c := &v1.GitRowChunk{
		ScanId: "scan-2", ChunkId: "c-last", Kind: v1.ChunkKind_BLOB,
		IsLastInScan: true,
		Rows: []*v1.GitRow{
			{LineNumber: 1, Content: []byte("nothing")},
		},
	}
	if err := h.Handle(context.Background(), c); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.final) != 1 || tr.final[0] != "scan-2" {
		t.Errorf("FinalEmit calls = %+v, want [scan-2]", tr.final)
	}
}

type partialPublisher struct {
	mu       sync.Mutex
	succeed  int
	calls    int
	findings []*v1.Finding
}

func (p *partialPublisher) PublishFinding(_ context.Context, f *v1.Finding) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls > p.succeed {
		return errors.New("simulated publish failure")
	}
	p.findings = append(p.findings, f)
	return nil
}

func TestHandle_IncrementsPerSuccessfulPublish(t *testing.T) {
	r := rules.Rule{
		ID: "test", Severity: v1.Severity_HIGH,
		Regex: regexp.MustCompile(`AKIA[A-Z0-9]{16}`),
	}
	d := detect.NewDetector([]rules.Rule{r}, "scanner/test")
	pub := &partialPublisher{succeed: 2}
	tr := &fakeTracker{}
	h := NewHandler(d, pub, tr)

	c := &v1.GitRowChunk{
		ScanId: "scan-fail", ChunkId: "c-1", Kind: v1.ChunkKind_BLOB,
		Rows: []*v1.GitRow{
			{LineNumber: 1, Content: []byte("AKIAIOSFODNN7EXAMPLE")},
			{LineNumber: 2, Content: []byte("AKIAIOSFODNN7EXAMPLE")},
			{LineNumber: 3, Content: []byte("AKIAIOSFODNN7EXAMPLE")},
		},
	}
	err := h.Handle(context.Background(), c)
	if err == nil {
		t.Fatal("Handle: want error from partial publisher, got nil")
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.incs["scan-fail"] != 2 {
		t.Errorf("Incr count = %d, want 2 (counter advances per successful publish, not batch)", tr.incs["scan-fail"])
	}
}

// timeout sanity — pool_test should not hang forever if implementation deadlocks.
func TestHandle_DoesNotHang(t *testing.T) {
	r := rules.Rule{ID: "test", Severity: v1.Severity_LOW, Regex: regexp.MustCompile(`.`)}
	d := detect.NewDetector([]rules.Rule{r}, "scanner/test")
	pub := &fakePublisher{}
	tr := &fakeTracker{}
	h := NewHandler(d, pub, tr)
	c := &v1.GitRowChunk{ScanId: "s", ChunkId: "c", Rows: []*v1.GitRow{{LineNumber: 1, Content: []byte("a")}}}
	done := make(chan error, 1)
	go func() { done <- h.Handle(context.Background(), c) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Handle: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Handle hung")
	}
}
