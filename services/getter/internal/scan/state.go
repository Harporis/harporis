package scan

import (
	"fmt"
	"sync"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// allowedTransitions maps current state → set of valid next states.
var allowedTransitions = map[v1.ScanState]map[v1.ScanState]bool{
	v1.ScanState_PENDING: {
		v1.ScanState_RUNNING:   true,
		v1.ScanState_FAILED:    true,
		v1.ScanState_CANCELLED: true,
	},
	v1.ScanState_RUNNING: {
		v1.ScanState_COMPLETED: true,
		v1.ScanState_PARTIAL:   true,
		v1.ScanState_FAILED:    true,
		v1.ScanState_CANCELLED: true,
	},
}

func isTerminal(s v1.ScanState) bool {
	switch s {
	case v1.ScanState_COMPLETED, v1.ScanState_PARTIAL,
		v1.ScanState_FAILED, v1.ScanState_CANCELLED:
		return true
	}
	return false
}

// stateGuard wraps a ScanState with a mutex.
type stateGuard struct {
	mu    sync.RWMutex
	state v1.ScanState
}

func (g *stateGuard) get() v1.ScanState {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.state
}

func (g *stateGuard) transition(next v1.ScanState) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if isTerminal(g.state) {
		return fmt.Errorf("scan already terminal (%s); cannot transition to %s", g.state, next)
	}
	allowed := allowedTransitions[g.state]
	if !allowed[next] {
		return fmt.Errorf("invalid transition: %s → %s", g.state, next)
	}
	g.state = next
	return nil
}
