package scan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistry_RegisterUnregister(t *testing.T) {
	reg := NewRegistry()

	sc := NewContext("a")
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, reg.Register("a", sc, cancel))

	// duplicate id → error
	require.ErrorIs(t, reg.Register("a", NewContext("a"), func() {}), ErrAlreadyExists)

	got, ok := reg.Get("a")
	require.True(t, ok)
	require.Same(t, sc, got)

	require.True(t, reg.Cancel("a", "user changed mind"))
	<-ctx.Done() // cancel func invoked
	require.Equal(t, "user changed mind", sc.CancelReason(),
		"reason from CancelScanRequest must reach the scan context")

	reg.Unregister("a")
	_, ok = reg.Get("a")
	require.False(t, ok)
}

func TestRegistry_CancelMissing(t *testing.T) {
	require.False(t, NewRegistry().Cancel("missing", "anything"))
}
