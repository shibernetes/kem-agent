//go:build !race

package shared

import (
	"testing"

	"github.com/shibernetes/kem-agent/event"
)

// TestAppendEventDoesNotAllocate asserts that the encoder allocates
// nothing per event.
//
// It is excluded under the race detector, where sync.Pool drops one
// value in four by design, so every fourth event rebuilds an encoder
// that should have been reused.
func TestAppendEventDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	var (
		enc = NewJSONEncoder()
		ev  = &event.Event{Type: "Warning", Reason: "OOMKilled"}
		buf = enc.AppendEvent(make([]byte, 0, 512), ev)
	)
	n := testing.AllocsPerRun(100, func() {
		buf = enc.AppendEvent(buf[:0], ev)
	})
	if n != 0 {
		t.Errorf("got %v allocations per event, want none", n)
	}
}
