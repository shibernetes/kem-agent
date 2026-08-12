//go:build !race

package pipeline

import (
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/event"
)

func TestDispatchDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	var (
		shared = &growingProducer{frame: 64}
		first  = testPipeline("a", shared)
		second = testPipeline("b", shared, &growingProducer{frame: 64})
	)
	f, _ := newFanout(t, time.Minute, matchAll(1), first, second)
	ev := &event.Event{Type: "Warning"}

	// The first event arms the evaluation deadline, which
	// is then reused until the refresh interval passes.
	ctx := t.Context()
	f.Dispatch(ctx, ev)

	n := testing.AllocsPerRun(100, func() {
		f.Dispatch(ctx, ev)
	})
	if n != 0 {
		t.Errorf("got %v allocations per event, want none", n)
	}
}
