package metrics

import "testing"

func TestSinkSeverityDroppedRegistered(t *testing.T) {
	Init()
	if SinkSeverityDropped == nil {
		t.Fatal("SinkSeverityDropped not initialized")
	}
	SinkSeverityDropped.WithLabelValues("HIGH").Inc()
}
