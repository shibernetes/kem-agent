//go:build !race

package otel

import (
	"testing"
)

// TestComposeDoesNotAllocate asserts that a framer reuses its buffers,
// so composing a batch allocates nothing after the first one.
func TestComposeDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	var (
		f      = newFramer()
		frames = appendFrames(notedEvent("first"), fullEvent(), notedEvent("second"))
		dst    = f.Compose(nil, frames, 3)
	)
	n := testing.AllocsPerRun(100, func() {
		dst = f.Compose(dst[:0], frames, 3)
	})
	if n != 0 {
		t.Errorf("got %v allocations per batch, want none", n)
	}
}
