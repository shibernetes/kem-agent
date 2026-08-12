package sink

import (
	"testing"
)

func TestBatchID(t *testing.T) {
	id := NewBatchID()
	first := id.String()

	// The retry loop carries one value across every attempt at a batch,
	// which a destination deduplicating on it depends on.
	for attempt := range 3 {
		if got := id.String(); got != first {
			t.Errorf("got %s on attempt %d, want %s", got, attempt+1, first)
		}
	}
	if other := NewBatchID(); other.String() == first {
		t.Error("got the same id for two batches, want each its own")
	}
}
