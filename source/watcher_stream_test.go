package source

import (
	"context"
	"errors"
	"slices"
	"testing"

	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/shibernetes/kem-agent/event"
)

// TestWatcherFrameTypes asserts which frames carry an event to deliver,
// and that every one of them advances the position. A position that moved
// only with the events delivered would stall on a quiet namespace until
// the APIServer refused to resume it.
func TestWatcherFrameTypes(t *testing.T) {
	cases := map[string]struct {
		frame      watch.EventType
		dispatched int
	}{
		"added":    {frame: watch.Added, dispatched: 1},
		"modified": {frame: watch.Modified, dispatched: 1},
		"deleted":  {frame: watch.Deleted},
		"bookmark": {frame: watch.Bookmark},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var (
				r = &eventRecorder{}
				m = newMetricsRecorder()
				w = newTestWatcher(t, watcherOptions{
					config:     WatchConfig{Namespace: testNamespace},
					dispatcher: r,
					metrics:    m.metrics(),
					resume:     "1000",
				})
			)
			err := drain(t, w, func(s *watch.FakeWatcher) {
				s.Action(tc.frame, watchedEvent("1042"))
			})
			if err != nil {
				t.Fatalf("the stream ended with an error: %v", err)
			}
			if got := len(r.names()); got != tc.dispatched {
				t.Errorf("got %d events dispatched, want %d", got, tc.dispatched)
			}
			if got := m.counts()["read"]; got != float64(tc.dispatched) {
				t.Errorf("got %v read, want %d", got, tc.dispatched)
			}
			if got := w.position(); got != "1042" {
				t.Errorf("got position %q, want %q", got, "1042")
			}
		})
	}
}

// TestWatcherDeletionIsNotDelivered asserts that the garbage collection
// of an event is not read as the event happening again. A deletion carries
// the whole object, so passing it on would deliver it a second time.
func TestWatcherDeletionIsNotDelivered(t *testing.T) {
	var (
		r = &eventRecorder{}
		w = newTestWatcher(t, watcherOptions{
			config:     WatchConfig{Namespace: testNamespace},
			dispatcher: r,
			resume:     "1000",
		})
	)
	err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Add(watchedEvent("1042"))
		s.Delete(watchedEvent("1042"))
	})
	if err != nil {
		t.Fatalf("the stream ended with an error: %v", err)
	}
	if got := r.names(); len(got) != 1 {
		t.Errorf("got %v, want the event delivered once", got)
	}
}

// TestWatcherErrorFrameEndsStream asserts that an error frame ends
// the stream and is classified, leaving whatever followed it unread.
func TestWatcherErrorFrameEndsStream(t *testing.T) {
	var (
		r = &eventRecorder{}
		w = newTestWatcher(t, watcherOptions{
			config:     WatchConfig{Namespace: testNamespace},
			dispatcher: r,
			resume:     "1000",
		})
	)
	err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Error(goneStatus())
		s.Add(watchedEvent("1042"))
	})
	if !errors.Is(err, errExpired) {
		t.Fatalf("got %v, want %v", err, errExpired)
	}
	if got := r.names(); len(got) != 0 {
		t.Errorf("got %v, want nothing read past the error", got)
	}
	if got := w.position(); got != "1000" {
		t.Errorf("got position %q, want it unmodified after the error", got)
	}
}

// TestWatcherReplaySuspendsPosition asserts that a replay records no
// position until the bookmark ending it. The APIServer serves the initial
// events in name order, so a position taken during one sits above events
// it has not reached, and a restart would skip them for good.
func TestWatcherReplaySuspendsPosition(t *testing.T) {
	var (
		r = &eventRecorder{}
		w = newTestWatcher(t, watcherOptions{
			config:     WatchConfig{Namespace: testNamespace},
			dispatcher: r,
		})
	)
	if err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Add(watchedEvent("1042"))
		s.Add(watchedEvent("1043"))
	}); err != nil {
		t.Fatalf("the stream ended with an error: %v", err)
	}
	if got := w.position(); got != "" {
		t.Fatalf("got position %q during a replay, want none recorded", got)
	}
	// The bookmark ending the replay covers everything it served,
	// and the stream is followed normally from there.
	if err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Add(watchedEvent("1044"))
		s.Action(watch.Bookmark, bookmarkFrame("1100", true))
		s.Add(watchedEvent("1101"))
	}); err != nil {
		t.Fatalf("the stream ended with an error: %v", err)
	}
	if got := w.position(); got != "1101" {
		t.Errorf("got position %q, want the stream followed past the replay", got)
	}
	if got := len(r.names()); got != 4 {
		t.Errorf("got %d events dispatched, want the replay delivered as well", got)
	}
}

// TestWatcherPeriodicBookmarkKeepsReplay asserts that only the bookmark
// carrying the initial events end annotation stops a replay. The APIServer
// sends bookmark events periodically regardless, and ending on those would
// record a position the replay has not reached.
func TestWatcherPeriodicBookmarkKeepsReplay(t *testing.T) {
	w := newTestWatcher(t, watcherOptions{
		config:     WatchConfig{Namespace: testNamespace},
		dispatcher: &eventRecorder{},
	})
	err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Action(watch.Bookmark, bookmarkFrame("1100", false))
	})
	if err != nil {
		t.Fatalf("the stream ended with an error: %v", err)
	}
	if !w.replay.active {
		t.Error("the replay ended on a periodic bookmark, want it still running")
	}
	if got := w.position(); got != "" {
		t.Errorf("got position %q, want none recorded during a replay", got)
	}
}

// TestWatcherLogsReplayCounts asserts that a replay reports what it
// kept and what it skipped.
func TestWatcherLogsReplayCounts(t *testing.T) {
	var (
		r = &logRecorder{}
		w = newTestWatcher(t, watcherOptions{
			config:     WatchConfig{Namespace: testNamespace},
			dispatcher: &eventRecorder{},
			logger:     r.logger(),
		})
	)
	w.replay.startAfter("1042")

	err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Add(watchedEvent("1042"))
		s.Add(watchedEvent("1043"))
		s.Action(watch.Bookmark, bookmarkFrame("1100", true))
	})
	if err != nil {
		t.Fatalf("the stream ended with an error: %v", err)
	}
	if got := r.count(); got != 1 {
		t.Fatalf("got %d lines, want the replay reported once", got)
	}
	if got := r.attr(t, 0, "replayed"); got != "1" {
		t.Errorf("the log record reports %q replayed events, want %q", got, "1")
	}
	if got := r.attr(t, 0, "skipped"); got != "1" {
		t.Errorf("the log record reports %q skipped events, want %q", got, "1")
	}
}

// TestWatcherReportsReplay asserts that a first replay is always reported,
// while one after an expired position is reported only when it delivered
// events the watch had missed. The routine one is only counted.
func TestWatcherReportsReplay(t *testing.T) {
	cases := map[string]struct {
		position string
		events   []string
		want     int
	}{
		"first replay":               {events: []string{"1042"}, want: 1},
		"replay that missed nothing": {position: "1042", events: []string{"1042"}},
		"replay that missed events":  {position: "1042", events: []string{"1043"}, want: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var (
				r = &logRecorder{}
				w = newTestWatcher(t, watcherOptions{
					config:     WatchConfig{Namespace: testNamespace},
					dispatcher: &eventRecorder{},
					logger:     r.logger(),
				})
			)
			if tc.position != "" {
				w.replay.startAfter(tc.position)
			}
			err := drain(t, w, func(s *watch.FakeWatcher) {
				for _, rv := range tc.events {
					s.Add(watchedEvent(rv))
				}
				s.Action(watch.Bookmark, bookmarkFrame("1100", true))
			})
			if err != nil {
				t.Fatalf("the stream ended with an error: %v", err)
			}
			if got := r.count(); got != tc.want {
				t.Errorf("got %d records, want %d", got, tc.want)
			}
		})
	}
}

// TestWatcherIgnoresUnknownObject asserts that a frame carrying
// something else than an event is skipped and don't advance position.
func TestWatcherIgnoresUnknownObject(t *testing.T) {
	var (
		r = &eventRecorder{}
		w = newTestWatcher(t, watcherOptions{
			config:     WatchConfig{Namespace: testNamespace},
			dispatcher: r,
			resume:     "1000",
		})
	)
	err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Action(watch.Added, &metav1.PartialObjectMetadata{})
	})
	if err != nil {
		t.Fatalf("the stream ended with an error: %v", err)
	}
	if got := r.names(); len(got) != 0 {
		t.Errorf("got %v, want nothing dispatched", got)
	}
	if got := w.position(); got != "1000" {
		t.Errorf("got position %q, want it unmodified", got)
	}
}

// TestWatcherEnrichesBeforeDispatch asserts that an event carries the metadata
// of the object it regards by the time the pipelines see it, since a filter can
// test that object and each event is dispatched once.
//
// The dispatcher reads the event as it is handed over rather than afterwards,
// because enriching it later would mutate the same event and leave a later read
// unable to tell the two orders apart.
func TestWatcherEnrichesBeforeDispatch(t *testing.T) {
	enriched := false

	var (
		e = &stampingEnricher{}
		d = dispatcherFunc(func(_ context.Context, ev *event.Event) {
			enriched = ev.RegardingObject != nil
		})
		w = newTestWatcher(t, watcherOptions{
			config:     WatchConfig{Namespace: testNamespace},
			dispatcher: d,
			enricher:   e,
			resume:     "1000",
		})
	)
	err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Action(watch.Added, watchedEvent("1042"))
	})
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}
	if !enriched {
		t.Error("the event carried no regarding object when it was dispatched")
	}
	if got := e.watches; !slices.Equal(got, []string{testNamespace}) {
		t.Errorf("the enricher received watch keys %v, want only %q", got, testNamespace)
	}
}

// drain reads a stream carrying only the queued frames.
// It closes the stream first, so the read consumes the buffer
// and stops rather than blocking.
func drain(t *testing.T, w *namespaceWatcher, frames func(*watch.FakeWatcher)) error {
	t.Helper()

	stream := watch.NewFakeWithChanSize(testStreamSize, false)
	frames(stream)
	stream.Stop()

	return w.read(t.Context(), stream)
}

// bookmarkFrame returns a bookmark object, which carries a resource
// version and no event. The annotation marks the one ending a replay.
func bookmarkFrame(rv string, initialEventsEnd bool) *eventsv1.Event {
	e := &eventsv1.Event{
		ResourceVersion: rv,
	}
	if initialEventsEnd {
		e.Annotations = map[string]string{metav1.InitialEventsAnnotationKey: "true"}
	}
	return e
}
