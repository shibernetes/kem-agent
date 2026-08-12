package source

import (
	"testing"
	"testing/synctest"
	"time"

	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
)

func TestReplayKeepsEverythingWhenInactive(t *testing.T) {
	var r replay

	if !r.keep(eventAt(testEventTime.Add(-time.Hour))) {
		t.Error("an event was dropped outside a replay, want it kept")
	}
	if kept, skipped := r.kept, r.skipped; kept != 0 || skipped != 0 {
		t.Errorf("got %d kept and %d skipped, want neither counted", kept, skipped)
	}
}

func TestReplayStartKeepsEverything(t *testing.T) {
	var r replay
	r.start()

	if !r.keep(eventAt(testEventTime.Add(-100 * time.Hour))) {
		t.Error("an old event was dropped, want the whole retained set kept")
	}
}

func TestReplayMaxAge(t *testing.T) {
	cases := map[string]struct {
		eventAge time.Duration
		want     bool
	}{
		"older than the limit": {eventAge: 2 * time.Hour},
		"inside the limit":     {eventAge: 30 * time.Minute, want: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := replay{maxAge: time.Hour}
				r.start()

				switch got := r.keep(eventAt(time.Now().Add(-tc.eventAge))); {
				case got && !tc.want:
					t.Error("an event older than the age limit was replayed, want it dropped")
				case !got && tc.want:
					t.Error("an event inside the age limit was dropped, want it replayed")
				}
			})
		})
	}
}

func TestReplayUsesOccurrenceTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// An event created long before the age limit, still recurring.
		// The replay measures the occurrence time, which a series carries
		// forward, so a condition that is still present is replayed.
		ev := eventAt(time.Now().Add(-10 * time.Hour))
		ev.Series = &eventsv1.EventSeries{
			Count: 4, LastObservedTime: metav1.NewMicroTime(time.Now()),
		}
		r := replay{maxAge: time.Hour}
		r.start()

		if !r.keep(ev) {
			t.Error("a recurring event was dropped, want it kept for its last observation")
		}
	})
}

func TestReplayEndReportsCounts(t *testing.T) {
	var r replay
	r.startAfter("1000")
	r.keep(eventWithVersion("1001"))
	r.keep(eventWithVersion("1002"))
	r.keep(eventWithVersion("999"))

	kept, skipped := r.end()
	if kept != 2 || skipped != 1 {
		t.Errorf("got %d kept and %d skipped, want 2 and 1", kept, skipped)
	}
	if !r.keep(eventWithVersion("999")) {
		t.Error("an event below the position was dropped after the replay ended, want it kept")
	}
}

func TestReplayRestartRetainsFloor(t *testing.T) {
	var r replay
	r.startAfter("1000")
	r.keep(eventWithVersion("1001"))
	r.restart()

	if !r.active {
		t.Error("the replay ended, want it still running")
	}
	if r.keep(eventWithVersion("999")) {
		t.Error("an event below the floor was kept, want the floor retained")
	}
	if kept, skipped := r.end(); kept != 0 || skipped != 1 {
		t.Errorf("got %d kept and %d skipped, want 0 and 1", kept, skipped)
	}
}

func TestReplayAfterPosition(t *testing.T) {
	cases := map[string]struct {
		rv   string
		want bool
	}{
		"below the position": {rv: "999"},
		"at the position":    {rv: "1000"},
		"above the position": {rv: "1001", want: true},
		"not comparable":     {rv: "abc", want: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var r replay
			r.startAfter("1000")

			switch got := r.keep(eventWithVersion(tc.rv)); {
			case got && !tc.want:
				t.Error("an event delivered before the replay was replayed, want it dropped")
			case !got && tc.want:
				t.Error("an event that may have been missed was dropped, want it replayed")
			}
		})
	}
}

func TestReplayAfterPositionIgnoresMaxAge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := replay{maxAge: time.Hour}
		r.startAfter("1000")

		ev := eventAt(time.Now().Add(-2 * time.Hour))
		ev.ResourceVersion = "1001"
		if !r.keep(ev) {
			t.Error("a missed event older than the age limit was dropped, want it replayed")
		}
	})
}

func eventAt(ts time.Time) *event.Event {
	return &event.Event{EventTime: metav1.NewMicroTime(ts)}
}

func eventWithVersion(rv string) *event.Event {
	return &event.Event{ResourceVersion: rv, EventTime: testEventTime}
}
