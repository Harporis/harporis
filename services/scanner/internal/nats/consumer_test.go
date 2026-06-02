package nats

import (
	"context"
	"sync"
	"testing"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestConsumer_DeliversChunkAndAcks(t *testing.T) {
	s := runJSServer(t)
	cl := dialAndEnsure(t, s)

	publishChunk(t, cl, "scan-A", &v1.GitRowChunk{
		ScanId:  "scan-A",
		ChunkId: "c1",
		Kind:    v1.ChunkKind_BLOB,
		Rows:    []*v1.GitRow{{LineNumber: 1, Content: []byte("hello")}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got *v1.GitRowChunk
	var wg sync.WaitGroup
	wg.Add(1)

	sub, err := NewChunksConsumer(cl.JS, ConsumerOptions{
		BatchSize:      1,
		FetchMaxWait:   500 * time.Millisecond,
		AckWaitSeconds: 5,
		MaxDeliver:     3,
		MaxAckPending:  8,
	})
	if err != nil {
		t.Fatalf("NewChunksConsumer: %v", err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	go func() {
		defer wg.Done()
		_ = sub.Run(ctx, func(_ context.Context, c *v1.GitRowChunk) error {
			got = c
			cancel()
			return nil
		})
	}()
	wg.Wait()

	if got == nil || got.ChunkId != "c1" {
		t.Fatalf("got chunk = %+v, want c1", got)
	}
}

func TestConsumer_NakOnHandlerError(t *testing.T) {
	s := runJSServer(t)
	cl := dialAndEnsure(t, s)

	publishChunk(t, cl, "scan-B", &v1.GitRowChunk{
		ScanId:  "scan-B",
		ChunkId: "c1",
		Kind:    v1.ChunkKind_BLOB,
		Rows:    []*v1.GitRow{{LineNumber: 1, Content: []byte("x")}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub, err := NewChunksConsumer(cl.JS, ConsumerOptions{
		BatchSize:      1,
		FetchMaxWait:   200 * time.Millisecond,
		AckWaitSeconds: 1,
		MaxDeliver:     3,
		MaxAckPending:  8,
	})
	if err != nil {
		t.Fatalf("NewChunksConsumer: %v", err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	var calls int
	var mu sync.Mutex
	done := make(chan struct{})

	go sub.Run(ctx, func(_ context.Context, c *v1.GitRowChunk) error {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n >= 2 {
			close(done)
			return nil
		}
		return errSimulated
	})
	<-done
	if calls < 2 {
		t.Errorf("handler called %d times, want >= 2 (redelivery after Nak)", calls)
	}
}

var errSimulated = simulatedErr("boom")

type simulatedErr string

func (e simulatedErr) Error() string { return string(e) }
