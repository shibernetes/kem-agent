package logsample

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestPerInterval(t *testing.T) {
	g := PerInterval[string](time.Hour, 2)

	admits(t, g, "a", true, true, false, false)

	// A different key opens a fresh interval.
	admits(t, g, "b", true, true, false)

	// The first key is new again, since only the last one is held.
	admits(t, g, "a", true)
}

func TestPerIntervalExpiry(t *testing.T) {
	g := PerInterval[string](time.Millisecond, 1)

	admits(t, g, "a", true, false)
	time.Sleep(2 * time.Millisecond)
	admits(t, g, "a", true, false)
}

func TestOnChange(t *testing.T) {
	g := OnChange[string]()

	admits(t, g, "a", true, false, false)
	admits(t, g, "b", true, false)
	admits(t, g, "a", true, false)
}

func TestFirstSeen(t *testing.T) {
	g := FirstSeen[string](2)

	admits(t, g, "a", true, false)
	admits(t, g, "b", true, false)

	// The cap is reached, so a third key is silenced for good.
	admits(t, g, "c", false, false)
	admits(t, g, "a", false)

	// A gate with no room admits nothing.
	admits(t, FirstSeen[string](0), "a", false, false)
}

func TestZeroKeyIsAdmitted(t *testing.T) {
	// The zero value of a key is undiscriminated and admitted
	// because a gate that has admitted nothing holds no state
	// at all, rather than a state whose key field is unset.
	admits(t, OnChange[string](), "", true, false)
	admits(t, FirstSeen[int](4), 0, true, false)
}

func TestSuppressedCount(t *testing.T) {
	g := OnChange[string]()

	if attr, ok := g.Admit("a"); !ok || withheld(t, attr) != 0 {
		t.Fatal("got the first occurrence withheld, want it admitted with nothing suppressed")
	}
	for range 3 {
		if _, ok := g.Admit("a"); ok {
			t.Fatal("got a repeated admission, want it withheld")
		}
	}
	attr, ok := g.Admit("b")
	if !ok {
		t.Fatal("got a different key withheld, want it admitted")
	}
	if n := withheld(t, attr); n != 3 {
		t.Errorf("got %d withheld, want 3", n)
	}
}

func TestClear(t *testing.T) {
	g := PerInterval[string](time.Hour, 1)

	if _, ok := g.Clear(); ok {
		t.Error("got a recovery before anything was admitted, want none")
	}
	g.Admit("a")
	g.Admit("a")

	attr, ok := g.Clear()
	if !ok {
		t.Fatal("got no recovery after an admitted occurrence, want one")
	}
	if n := withheld(t, attr); n != 1 {
		t.Errorf("got %d withheld, want 1", n)
	}
	// The gate starts over, so the condition returning logs at once.
	admits(t, g, "a", true, false)
}

func TestConcurrentAdmit(t *testing.T) {
	gates := map[string]*Gate[int]{
		"per interval": PerInterval[int](time.Hour, 1),
		"on change":    OnChange[int](),
		"first seen":   FirstSeen[int](8),
	}
	for name, g := range gates {
		t.Run(name, func(t *testing.T) {
			var wg sync.WaitGroup
			for i := range 8 {
				wg.Go(func() {
					for range 100 {
						g.Admit(i % 4)
						g.Clear()
					}
				})
			}
			wg.Wait()
		})
	}
}

// withheld returns the count an admitted attribute carries,
// asserting the one spelling every gate emits it under.
func withheld(t *testing.T, attr slog.Attr) int64 {
	t.Helper()

	const key = "suppressed"
	if attr.Key != key {
		t.Errorf("got attribute key %q, want %q", attr.Key, key)
	}
	return attr.Value.Int64()
}

// admits asserts the gate's verdict for a run of occurrences of one key.
func admits[K comparable](t *testing.T, g *Gate[K], key K, want ...bool) {
	t.Helper()

	for i, w := range want {
		if _, ok := g.Admit(key); ok != w {
			t.Errorf("occurrence %d of %v: got admitted %v, want %v", i+1, key, ok, w)
		}
	}
}
