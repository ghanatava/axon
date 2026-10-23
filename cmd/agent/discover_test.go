//go:build linux && amd64

package main

import (
	"github.com/ghanatava/axon/internal/session"
	"testing"
)

func TestTelemetryCountersSurviveWorkerReplacement(t *testing.T) {
	m := &nodeAgent{}
	first := &child{}
	m.updateTelemetry(first, telemetry{Runtime: session.Metrics{Opened: 3, Closed: 2, Invalidated: 1}, Capture: [6]uint64{0, 0, 4}})
	// Repeated snapshots must not duplicate counters.
	m.updateTelemetry(first, first.last)
	m.updateTelemetry(first, telemetry{Runtime: session.Metrics{Opened: 4, Closed: 3, Invalidated: 1}, Capture: [6]uint64{0, 0, 5}})
	replacement := &child{}
	m.updateTelemetry(replacement, telemetry{Runtime: session.Metrics{Opened: 2, Closed: 1}, Capture: [6]uint64{0, 0, 1}})
	if m.totals.Runtime.Opened != 6 || m.totals.Runtime.Closed != 4 || m.totals.Runtime.Invalidated != 1 || m.totals.Capture[2] != 6 {
		t.Fatal(m.totals)
	}
}
