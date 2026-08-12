package source

import (
	"time"

	"k8s.io/apimachinery/pkg/util/resourceversion"

	"github.com/shibernetes/kem-agent/event"
)

// replay tracks the initial events of a watch, and decides which of
// them are delivered. A watch holding no position replays everything
// the APIServer retains.
type replay struct {
	maxAge  time.Duration
	floor   string
	oldest  time.Time
	active  bool
	kept    int
	skipped int
}

// start starts a replay of every event the APIServer retains, which is how a
// watch with no position starts.
//
// When an event age limit is set, the replay drops the events older than that
// limit. A repeating event is dated by its last observation, so one that is
// still recurring is kept.
func (r *replay) start() {
	r.active = true
	r.floor = ""
	r.oldest = time.Time{}
	r.restart()

	if r.maxAge > 0 {
		r.oldest = time.Now().Add(-r.maxAge)
	}
}

// startAfter starts a replay of the events with a resource version newer
// than the given one, which were not delivered before the replay started.
func (r *replay) startAfter(resourceVersion string) {
	r.active = true
	r.floor = resourceVersion
	r.oldest = time.Time{}
	r.restart()
}

// end ends the replay. It returns the number of events
// delivered and dropped.
func (r *replay) end() (int, int) {
	r.active = false
	return r.kept, r.skipped
}

// restart discards the counts of a replay served again
// from the beginning.
func (r *replay) restart() {
	r.kept = 0
	r.skipped = 0
}

// keep reports whether an event should be delivered.
func (r *replay) keep(ev *event.Event) bool {
	if !r.active {
		return true
	}
	if !r.missed(ev) {
		r.skipped++
		return false
	}
	r.kept++

	return true
}

// missed reports whether an event was not delivered before the replay started.
func (r *replay) missed(ev *event.Event) bool {
	if r.floor == "" {
		return r.oldest.IsZero() || !ev.Time().Before(r.oldest)
	}
	// Event resource versions increase with every write, so an event at or
	// below the floor was already delivered. An event whose resource version
	// cannot be compared is delivered rather than lost.
	cmp, err := resourceversion.CompareResourceVersion(ev.ResourceVersion, r.floor)
	return err != nil || cmp > 0
}
