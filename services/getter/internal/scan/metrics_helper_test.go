package scan

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	io_prometheus_client "github.com/prometheus/client_model/go"

	"github.com/Harporis/harporis/services/getter/internal/metrics"
)

// readCounterValue returns the float64 value of a labelled prometheus
// counter from the metrics-package custom registry. The second label
// argument is the non-scan-id label (reason/kind); pass "" if the metric
// has no second label.
//
// Implementation: iterate the registry's collected metrics, find the one
// whose name matches, and sum the value across label sets that include
// scanID (and the second label if non-empty).
func readCounterValue(t *testing.T, name, scanID, secondLabel string) float64 {
	t.Helper()
	mfs, err := metrics.Registry().Gather()
	require.NoError(t, err)
	var total float64
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if !labelMatch(m.GetLabel(), scanID, secondLabel) {
				continue
			}
			total += counterOrUntyped(m)
		}
		break
	}
	return total
}

func labelMatch(labels []*io_prometheus_client.LabelPair, scanID, second string) bool {
	matchedScan := scanID == ""
	matchedSecond := second == ""
	for _, lp := range labels {
		if lp.GetName() == "scan_id" && lp.GetValue() == scanID {
			matchedScan = true
		}
		// second is matched by any label whose value equals it (reason/kind/status).
		if second != "" && lp.GetValue() == second && !strings.EqualFold(lp.GetName(), "scan_id") {
			matchedSecond = true
		}
	}
	return matchedScan && matchedSecond
}

func counterOrUntyped(m *io_prometheus_client.Metric) float64 {
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	return 0
}
