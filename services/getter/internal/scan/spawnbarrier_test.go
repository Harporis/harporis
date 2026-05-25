package scan

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// waitFirstSpawn must return as soon as ONE worker reports success,
// regardless of how slow that worker was relative to others.
func TestWaitFirstSpawn_SucceedsOnFirstReport(t *testing.T) {
	sig := make(chan bool, 3)
	go func() {
		sig <- false
		time.Sleep(20 * time.Millisecond)
		sig <- true // success arrives later but should unblock
		sig <- false
	}()
	require.True(t, waitFirstSpawn(sig, 3))
}

// All workers reporting failure must yield false.
func TestWaitFirstSpawn_AllFailedReturnsFalse(t *testing.T) {
	sig := make(chan bool, 3)
	sig <- false
	sig <- false
	sig <- false
	require.False(t, waitFirstSpawn(sig, 3))
}

// Slow workers must NOT cause a false-positive cancellation: even with a
// 500ms first-report delay (well past the old 100ms hard sleep), the
// barrier must wait for the actual signal.
func TestWaitFirstSpawn_SlowSuccessStillCounts(t *testing.T) {
	sig := make(chan bool, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)
		sig <- true
	}()
	start := time.Now()
	require.True(t, waitFirstSpawn(sig, 1))
	require.GreaterOrEqual(t, time.Since(start), 400*time.Millisecond,
		"barrier must wait for the actual signal, not a hardcoded short timer")
}
