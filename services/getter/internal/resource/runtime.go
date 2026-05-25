package resource

import (
	"runtime"
	"runtime/debug"
)

type Limits struct {
	MaxCPUCores int
	MaxRAMMB    int
}

// ApplyLimits configures GOMAXPROCS and GOMEMLIMIT according to cfg.
// 0 means "do not override" — leave Go defaults.
func ApplyLimits(l Limits) {
	if l.MaxCPUCores > 0 {
		runtime.GOMAXPROCS(l.MaxCPUCores)
	}
	if l.MaxRAMMB > 0 {
		debug.SetMemoryLimit(int64(l.MaxRAMMB) * 1024 * 1024)
	}
}
