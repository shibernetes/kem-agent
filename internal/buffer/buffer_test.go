package buffer

import (
	"testing"
)

func TestShrink(t *testing.T) {
	const threshold = 1024
	cases := map[string]struct {
		length   int
		capacity int
		wantKept bool
	}{
		"empty":                 {0, 0, true},
		"small and idle":        {1, 512, true},
		"at the threshold":      {1, threshold, true},
		"past it and idle":      {1, threshold + 1, false},
		"past it and a quarter": {threshold / 4, threshold + 1, true},
		"past it and in use":    {threshold, threshold + 1, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := make([]byte, tc.length, tc.capacity)
			got := Shrink(b, threshold)
			switch {
			case tc.wantKept && cap(got) != tc.capacity:
				t.Errorf("got a buffer of capacity %d, want the %d it had", cap(got), tc.capacity)
			case !tc.wantKept && got != nil:
				t.Errorf("got a buffer of capacity %d, want it released", cap(got))
			}
		})
	}
}

func TestShrinkAcceptsNil(t *testing.T) {
	// A released buffer comes back nil, which is what the
	// caller hands to the next call.
	if got := Shrink(nil, 1024); got != nil {
		t.Errorf("got a buffer of capacity %d, want nil", cap(got))
	}
}

func TestShrinkKeepsContent(t *testing.T) {
	b := append(make([]byte, 0, 8), "kept"...)

	// An untouched buffer is returned as-is, since a caller
	// assigns the result back over the one it passed in.
	if got := string(Shrink(b, 1024)); got != "kept" {
		t.Errorf("got %q, want the buffer returned unchanged", got)
	}
}
