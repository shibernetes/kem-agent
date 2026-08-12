//go:build !race

package logsample

import (
	"testing"
	"time"
)

func TestWithheldOccurrenceDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	gates := map[string]*Gate[string]{
		"per interval": PerInterval[string](time.Hour, 1),
		"on change":    OnChange[string](),
		"first seen":   FirstSeen[string](4),
	}
	for name, g := range gates {
		t.Run(name, func(t *testing.T) {
			g.Admit("a")
			if n := testing.AllocsPerRun(100, func() { g.Admit("a") }); n != 0 {
				t.Errorf("got %v allocations per withheld occurrence, want 0", n)
			}
		})
	}
}
