package nats

import (
	"context"
	"testing"
	"time"

	natsclient "github.com/nats-io/nats.go"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// scriptedFetcher replays a fixed sequence of Fetch results, one per call.
// On the final scripted result it cancels ctx so runStatusLoop exits.
type scriptedFetcher struct {
	results []fetchResult
	calls   int
	cancel  context.CancelFunc
}

type fetchResult struct {
	msgs []*natsclient.Msg
	err  error
}

func (f *scriptedFetcher) Fetch(_ int, _ ...natsclient.PullOpt) ([]*natsclient.Msg, error) {
	i := f.calls
	if i >= len(f.results)-1 {
		f.cancel() // last result: stop the loop after this returns
		i = len(f.results) - 1
	}
	f.calls++
	r := f.results[i]
	return r.msgs, r.err
}

func noopTerminal(context.Context, string, v1.ScanState) error { return nil }

// recordingAfter returns a fake time.After that records each backoff
// duration and fires immediately so the test never actually sleeps.
func recordingAfter(rec *[]time.Duration) func(time.Duration) <-chan time.Time {
	return func(d time.Duration) <-chan time.Time {
		*rec = append(*rec, d)
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
}

// TestRunStatusLoop_BacksOffOnNonTimeoutError is the regression guard for the
// disk-eating busy-loop: ErrNoResponders (JetStream consumer/stream gone)
// returns from Fetch immediately, so without backoff the loop spins thousands
// of times a second and floods the container log. Assert the loop applies
// exponential backoff capped at 5s.
func TestRunStatusLoop_BacksOffOnNonTimeoutError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	results := make([]fetchResult, 8)
	for i := range results {
		results[i] = fetchResult{err: natsclient.ErrNoResponders}
	}
	f := &scriptedFetcher{results: results, cancel: cancel}

	var backoffs []time.Duration
	if err := runStatusLoop(ctx, f, noopTerminal, recordingAfter(&backoffs)); err != nil {
		t.Fatalf("runStatusLoop returned error: %v", err)
	}

	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		3200 * time.Millisecond,
		5 * time.Second, // 6400ms capped to 5s
	}
	if len(backoffs) != len(want) {
		t.Fatalf("recorded %d backoffs %v, want %d %v", len(backoffs), backoffs, len(want), want)
	}
	for i := range want {
		if backoffs[i] != want[i] {
			t.Errorf("backoff[%d] = %v, want %v", i, backoffs[i], want[i])
		}
	}
}

// TestRunStatusLoop_ResetsAfterSuccess verifies a successful fetch (even an
// empty batch) resets the backoff so a recovered server doesn't keep paying
// the accumulated penalty.
func TestRunStatusLoop_ResetsAfterSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &scriptedFetcher{cancel: cancel, results: []fetchResult{
		{err: natsclient.ErrNoResponders},
		{err: natsclient.ErrNoResponders},
		{msgs: nil},                        // success, empty batch -> reset
		{err: natsclient.ErrNoResponders},  // backoff restarts at base
		{err: natsclient.ErrNoResponders},  // last -> cancels, not recorded
	}}

	var backoffs []time.Duration
	if err := runStatusLoop(ctx, f, noopTerminal, recordingAfter(&backoffs)); err != nil {
		t.Fatalf("runStatusLoop returned error: %v", err)
	}

	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 100 * time.Millisecond}
	if len(backoffs) != len(want) {
		t.Fatalf("recorded %v, want %v", backoffs, want)
	}
	for i := range want {
		if backoffs[i] != want[i] {
			t.Errorf("backoff[%d] = %v, want %v", i, backoffs[i], want[i])
		}
	}
}

// TestRunStatusLoop_TimeoutDoesNotBackOff confirms the benign idle path
// (ErrTimeout from a MaxWait fetch) never triggers backoff.
func TestRunStatusLoop_TimeoutDoesNotBackOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &scriptedFetcher{cancel: cancel, results: []fetchResult{
		{err: natsclient.ErrTimeout},
		{err: natsclient.ErrTimeout},
		{err: natsclient.ErrTimeout},
	}}

	var backoffs []time.Duration
	if err := runStatusLoop(ctx, f, noopTerminal, recordingAfter(&backoffs)); err != nil {
		t.Fatalf("runStatusLoop returned error: %v", err)
	}
	if len(backoffs) != 0 {
		t.Fatalf("timeout path recorded backoffs %v, want none", backoffs)
	}
}
