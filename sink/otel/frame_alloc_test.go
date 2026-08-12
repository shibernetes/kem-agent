//go:build !race

package otel

import (
	"testing"
)

// TestAppendEventDoesNotAllocate asserts that the encoder allocates nothing
// per event, whatever metadata it holds. It is excluded under the race detector,
// where sync.Pool drops one value in four by design, so every fourth event
// rebuilds a record that should have been reused otherwise.
func TestAppendEventDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	var (
		enc = newEncoder(testAgentMetadata())
		ev  = fullEvent()
		buf = enc.AppendEvent(make([]byte, 0, 4<<10), ev) // 4 KiB
	)
	n := testing.AllocsPerRun(100, func() {
		buf = enc.AppendEvent(buf[:0], ev)
	})
	if n != 0 {
		t.Errorf("got %v allocations per event, want none", n)
	}
}
