package resource

import (
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyLimits_SetsGOMAXPROCS(t *testing.T) {
	orig := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(orig)

	ApplyLimits(Limits{MaxCPUCores: 2, MaxRAMMB: 0})
	require.Equal(t, 2, runtime.GOMAXPROCS(0))
}

func TestApplyLimits_SetsMemoryLimit(t *testing.T) {
	defer debug.SetMemoryLimit(-1) // unlimited

	ApplyLimits(Limits{MaxCPUCores: 0, MaxRAMMB: 256})
	cur := debug.SetMemoryLimit(-2) // -2 = read current without changing
	require.Equal(t, int64(256*1024*1024), cur)
}
