//go:build !race

package sanitizer

import (
	"testing"
)

func TestFieldLimitsOnShortFieldsDoesNotAllocate(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes the allocation profile")
	}
	original := shortFieldsEvent()
	avgAlloc := testing.AllocsPerRun(100, func() {
		e := original
		fieldLimits{}.Sanitize(&e)
	})
	if avgAlloc != 0 {
		t.Errorf("got %v allocations per event, want 0", avgAlloc)
	}
}
