package scan

import (
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestStateTransitions(t *testing.T) {
	sc := NewContext("scan-1")
	require.Equal(t, v1.ScanState_PENDING, sc.State())

	require.NoError(t, sc.Transition(v1.ScanState_RUNNING))
	require.Equal(t, v1.ScanState_RUNNING, sc.State())

	require.NoError(t, sc.Transition(v1.ScanState_COMPLETED))
	require.True(t, sc.IsTerminal())

	// terminal → any further transition is rejected
	require.Error(t, sc.Transition(v1.ScanState_FAILED))
}

func TestInvalidTransition(t *testing.T) {
	sc := NewContext("scan-2")
	require.Error(t, sc.Transition(v1.ScanState_COMPLETED)) // PENDING → COMPLETED skips RUNNING
}
