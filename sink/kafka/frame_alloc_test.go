//go:build !race

package kafka

import (
	"testing"
)

func TestAppendEventDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	var (
		enc = encoder{key: MessageKeyUID}
		ev  = testEvent()
		buf = enc.AppendEvent(nil, ev)
	)
	n := testing.AllocsPerRun(100, func() {
		buf = enc.AppendEvent(buf[:0], ev)
	})
	if n != 0 {
		t.Errorf("got %v allocations per event, want none", n)
	}
}
