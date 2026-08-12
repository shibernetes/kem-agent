//go:build !race

package metrics

import (
	"testing"
)

// TestHandleDoesNotAllocate asserts that recording an event allocates
// nothing, whatever the number of metrics.
func TestHandleDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	var (
		s  = newTestSink(t, allocConfig(0))
		ev = testEvent()
	)
	if err := s.Handle(ev); err != nil {
		t.Fatalf("the event was not recorded: %v", err)
	}
	n := testing.AllocsPerRun(1000, func() {
		_ = s.Handle(ev)
	})
	if n != 0 {
		t.Errorf("got %v allocations per event, want none", n)
	}
}

// TestHandleUnderLimitDoesNotAllocate asserts that recording an event
// allocates nothing when the sink keeps a series limit, which looks up
// its admitted set once per metric.
//
// The key is built in the pooled scratch buffer and read back without
// touching the heap, so the limit costs a lookup and no allocation.
func TestHandleUnderLimitDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	var (
		s  = newTestSink(t, allocConfig(1000))
		ev = testEvent()
	)
	// The series are admitted first, so the run measures the
	// steady state rather than the allocation a new series costs.
	if err := s.Handle(ev); err != nil {
		t.Fatalf("the event was not recorded: %v", err)
	}
	n := testing.AllocsPerRun(1000, func() {
		_ = s.Handle(ev)
	})
	if n != 0 {
		t.Errorf("got %v allocations per event, want none", n)
	}
}

// allocConfig returns a configuration declaring three metrics of
// differing width, so that the scratch buffer is sized to the largest
// and reused by the others.
func allocConfig(maxSeries int) Config {
	return testConfig(func(c *Config) {
		c.MaxSeries = maxSeries
		c.Metrics = []Metric{
			{Name: "by_namespace", Help: "h", Labels: map[string]string{"ns": "namespace"}},
			{Name: "by_reason", Help: "h", Labels: map[string]string{"reason": "reason", "ns": "namespace"}},
			{Name: "by_kind", Help: "h", Labels: map[string]string{"kind": "regarding.kind", "reason": "reason", "ns": "namespace"}},
		}
	})
}
