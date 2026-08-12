package logsample

import (
	"log/slog"
	"maps"
	"sync/atomic"
	"time"
)

// Gate controls whether an occurrence should be logged, and how many were
// withheld since the last one that was. It holds no logger and no policy,
// the caller keeps both, so a suppressed occurrence builds no record and
// evaluates no attribute.
//
// A Gate takes no lock and is safe for concurrent use. Only an admitted
// occurrence allocates, which is the one already about to build a log
// record. Two goroutines racing on the same key may both be admitted,
// and the withheld count is approximate under contention. Neither costs
// more than a repeated line, which is what the gate exists to bound rather
// than to forbid.
type Gate[K comparable] struct {
	interval  time.Duration
	threshold int64
	limit     int
	trackKeys bool
	state     atomic.Pointer[state[K]]
	withheld  atomic.Int64
}

// state tracks a log admission.
type state[K comparable] struct {
	key      K
	since    time.Time
	count    int64
	admitted map[K]struct{}
}

// PerInterval admits the first occurrences of a key up to the threshold
// per interval, then stays silent until the duration elapses. A different
// key is admitted at once and opens a fresh interval. A threshold above
// one is useful when the first few occurrences differ with useful detail,
// a status code or a backend, and one line does not show the shape.
func PerInterval[K comparable](interval time.Duration, threshold int) *Gate[K] {
	return &Gate[K]{interval: interval, threshold: int64(threshold)}
}

// OnChange admits an occurrence whose key differs from the last admitted
// one, and never expires. It is what reports a sequence whose every step
// is worth a log, while a repeated step is worth none.
func OnChange[K comparable]() *Gate[K] {
	return &Gate[K]{threshold: 1}
}

// FirstSeen admits the first occurrence of each distinct key, up to limit
// distinct keys, and then stays silent. The limit bounds how many keys are
// held, but nothing bounds how large one might be, so a caller whose keys
// carry data an event's author writes must bound their size itself.
func FirstSeen[K comparable](limit int) *Gate[K] {
	return &Gate[K]{limit: limit, trackKeys: true}
}

// Admit reports whether the occurrence corresponding to the given key
// should be logged, and how many were withheld since the last one that
// was. The returned attribute is zero unless the occurrence is admitted.
func (g *Gate[K]) Admit(key K) (slog.Attr, bool) {
	for {
		old := g.state.Load()
		next, ok := g.decide(old, key)
		if !ok {
			g.withheld.Add(1)
			return slog.Attr{}, false
		}
		if g.state.CompareAndSwap(old, next) {
			return suppressed(g.withheld.Swap(0)), true
		}
	}
}

// Clear reports whether the condition has ended and how many occurrences
// were withheld, so a caller can log the recovery. It reports false when
// the gate admitted nothing, which is what keeps a recovery line from
// following a failure that never happened.
//
// On a FirstSeen gate it also empties the set, so every key already
// admitted becomes admissible again.
func (g *Gate[K]) Clear() (slog.Attr, bool) {
	for {
		old := g.state.Load()
		if old == nil {
			return slog.Attr{}, false
		}
		if g.state.CompareAndSwap(old, nil) {
			return suppressed(g.withheld.Swap(0)), true
		}
	}
}

// decide returns the state that admitting the key would leave behind, and
// whether it is admitted at all. It allocates only on the admitting path.
func (g *Gate[K]) decide(old *state[K], key K) (*state[K], bool) {
	if g.trackKeys {
		return g.decideFirstSeen(old, key)
	}
	now := time.Now()

	// A gate holding no state has admitted nothing, which is what tells
	// a first occurrence from a repeat when the key is its own zero value.
	// A different key and an expired window open a fresh interval on the
	// same terms, rather than continuing the current count.
	if old == nil || key != old.key || (g.interval > 0 && now.Sub(old.since) >= g.interval) {
		return &state[K]{key: key, since: now, count: 1}, true
	}
	if old.count >= g.threshold {
		return nil, false
	}
	return &state[K]{
		key:   key,
		since: old.since,
		count: old.count + 1,
	}, true
}

// decideFirstSeen admits a key the gate has not seen, while it still has
// room for one. The admitted keys set is copied rather than written to.
func (g *Gate[K]) decideFirstSeen(old *state[K], key K) (*state[K], bool) {
	if old != nil {
		_, seen := old.admitted[key]
		if seen || len(old.admitted) >= g.limit {
			return nil, false
		}
	}
	if g.limit <= 0 {
		return nil, false
	}
	size := 1
	if old != nil {
		size += len(old.admitted)
	}
	next := &state[K]{admitted: make(map[K]struct{}, size)}
	if old != nil {
		maps.Copy(next.admitted, old.admitted)
	}
	next.admitted[key] = struct{}{}

	return next, true
}

// suppressed returns a log attribute carrying the withheld occurrence count.
func suppressed(n int64) slog.Attr {
	return slog.Int64("suppressed", n)
}
