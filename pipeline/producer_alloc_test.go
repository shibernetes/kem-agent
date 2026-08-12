//go:build !race

package pipeline

import (
	"testing"
)

func TestBatchProducerDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	var (
		d, _ = newTestDrainer(t, newFakeBatchSink(), testConfig())
		p    = d.Producer()
		ev   = testEvent(128)
		buf  = make([]byte, 0, 256)
	)
	got := testing.AllocsPerRun(100, func() {
		buf = p.Produce(buf, ev)
	})
	if got != 0 {
		t.Errorf("got %v allocations per event, want none", got)
	}
}
