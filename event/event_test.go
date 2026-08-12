package event

import (
	"testing"
	"time"

	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEventTime(t *testing.T) {
	var (
		observed = time.Date(2009, 1, 3, 10, 30, 0, 0, time.UTC)
		recorded = time.Date(2009, 1, 3, 9, 0, 0, 0, time.UTC)
	)
	cases := map[string]struct {
		event Event
		want  time.Time
	}{
		"original event time": {
			event: Event{EventTime: metav1.NewMicroTime(recorded)},
			want:  recorded,
		},
		"a repeating event reports its last observation": {
			event: Event{
				EventTime: metav1.NewMicroTime(recorded),
				Series:    &eventsv1.EventSeries{Count: 6, LastObservedTime: metav1.NewMicroTime(observed)},
			},
			want: observed,
		},
		"a series with no observation falls through": {
			event: Event{
				EventTime: metav1.NewMicroTime(recorded),
				Series:    &eventsv1.EventSeries{Count: 4},
			},
			want: recorded,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.event.Time(); !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
