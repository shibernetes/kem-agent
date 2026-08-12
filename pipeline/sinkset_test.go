package pipeline

import (
	"testing"
)

func TestSinkSetMarksOnce(t *testing.T) {
	s := newSinkSet(4)
	if !s.mark(2) {
		t.Fatal("the first mark reported the sink as already reached")
	}
	if s.mark(2) {
		t.Error("the second mark reported the sink as newly reached")
	}
}

// TestSinkSetMarksIndependently covers a set spanning two words,
// where marking a sink must leave the ones in another word alone.
func TestSinkSetMarksIndependently(t *testing.T) {
	s := newSinkSet(65)
	s.mark(0)

	if !s.mark(64) {
		t.Error("marking the first sink reached the one in the next word")
	}
}

func TestSinkSetClears(t *testing.T) {
	s := newSinkSet(4)
	s.mark(1)
	s.clear()

	if !s.mark(1) {
		t.Error("a cleared set still reported the sink as reached")
	}
}

func TestNewSinkSetSizes(t *testing.T) {
	cases := map[string]struct {
		sinks int
		want  int
	}{
		"none":            {0, 0},
		"one":             {1, 1},
		"full word":       {64, 1},
		"one past a word": {65, 2},
		"two full words":  {128, 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := len(newSinkSet(tc.sinks)); got != tc.want {
				t.Errorf("got %d words for %d sinks, want %d", got, tc.sinks, tc.want)
			}
		})
	}
}
