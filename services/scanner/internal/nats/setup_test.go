package nats

import (
	"os"
	"testing"

	"github.com/Harporis/harporis/services/scanner/internal/metrics"
)

func TestMain(m *testing.M) {
	metrics.Init()
	os.Exit(m.Run())
}
