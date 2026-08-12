package source

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
)

var (
	testEventTime  = metav1.NewMicroTime(time.Date(2009, time.January, 3, 10, 0, 0, 0, time.UTC))
	testLegacyTime = metav1.NewTime(time.Date(2009, time.January, 3, 9, 0, 0, 0, time.UTC))
)

// TestLiftFields compares a whole event rather than its fields one by one,
// so a field added to the canonical type and left out of the lift fails.
func TestLiftFields(t *testing.T) {
	upstream := testUpstreamEvent()

	want := &event.Event{
		Labels:              map[string]string{"app": "web"},
		Annotations:         map[string]string{"team": "hosting"},
		UID:                 "d8f1",
		ResourceVersion:     "1042",
		Namespace:           "team-a",
		Name:                "web.17f0",
		EventTime:           testEventTime,
		Series:              &eventsv1.EventSeries{Count: 4, LastObservedTime: testEventTime},
		ReportingController: "kubelet",
		ReportingInstance:   "node-001",
		Action:              "Killing",
		Reason:              "OOMKilled",
		Regarding: corev1.ObjectReference{
			Kind:      "Pod",
			Namespace: "team-a",
			Name:      "web",
		},
		Related: &corev1.ObjectReference{
			Kind: "Node",
			Name: "node-001",
		},
		Note: "container exceeded its memory limit",
		Type: "Warning",
	}
	if diff := cmp.Diff(want, lift(upstream)); diff != "" {
		t.Errorf("the lifted event differs (-want +got):\n%s", diff)
	}
}

// TestLiftSharesMetadata asserts that the lift reads the decoded object
// rather than copying it, which is what keeps it to a single allocation
// per event.
func TestLiftSharesMetadata(t *testing.T) {
	upstream := testUpstreamEvent()
	ev := lift(upstream)

	ev.Labels["app"] = "api"
	ev.Annotations["team"] = "platform"

	if got := upstream.Labels["app"]; got != "api" {
		t.Errorf("got label %q, want the lift to share the map", got)
	}
	if got := upstream.Annotations["team"]; got != "platform" {
		t.Errorf("got annotation %q, want the lift to share the map", got)
	}
}

func TestLiftPromotesDeprecatedSource(t *testing.T) {
	cases := map[string]struct {
		controller     string
		instance       string
		source         corev1.EventSource
		wantController string
		wantInstance   string
	}{
		"neither":            {},
		"modern":             {controller: "kubelet", instance: "node-001", wantController: "kubelet", wantInstance: "node-001"},
		"legacy":             {source: corev1.EventSource{Component: "kubelet", Host: "node-001"}, wantController: "kubelet", wantInstance: "node-001"},
		"component alone":    {source: corev1.EventSource{Component: "kubelet"}, wantController: "kubelet"},
		"host alone":         {source: corev1.EventSource{Host: "node-001"}, wantInstance: "node-001"},
		"modern over legacy": {controller: "csi", instance: "node-002", source: corev1.EventSource{Component: "kubelet", Host: "node-001"}, wantController: "csi", wantInstance: "node-002"},
		"one of each":        {controller: "csi", source: corev1.EventSource{Component: "kubelet", Host: "node-001"}, wantController: "csi", wantInstance: "node-001"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ev := lift(&eventsv1.Event{
				EventTime:           testEventTime,
				ReportingController: tc.controller,
				ReportingInstance:   tc.instance,
				DeprecatedSource:    tc.source,
			})
			if ev.ReportingController != tc.wantController {
				t.Errorf("got controller %q, want %q", ev.ReportingController, tc.wantController)
			}
			if ev.ReportingInstance != tc.wantInstance {
				t.Errorf("got instance %q, want %q", ev.ReportingInstance, tc.wantInstance)
			}
		})
	}
}

func TestLiftPromotesDeprecatedTimestamp(t *testing.T) {
	cases := map[string]struct {
		eventTime metav1.MicroTime
		first     metav1.Time
		want      time.Time
	}{
		"modern":             {eventTime: testEventTime, want: testEventTime.Time},
		"legacy":             {first: testLegacyTime, want: testLegacyTime.Time},
		"modern over legacy": {eventTime: testEventTime, first: testLegacyTime, want: testEventTime.Time},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ev := lift(&eventsv1.Event{
				EventTime:                tc.eventTime,
				DeprecatedFirstTimestamp: tc.first,
			})
			if !ev.EventTime.Time.Equal(tc.want) {
				t.Errorf("got event time %s, want %s", ev.EventTime, tc.want)
			}
		})
	}
}

// TestLiftSeries asserts that a repeating event reports its count, whichever
// API wrote it. A series is left nil below a count of two, so an event that
// occurred once is not reported as a series of one.
func TestLiftSeries(t *testing.T) {
	modern := &eventsv1.EventSeries{Count: 4, LastObservedTime: testEventTime}

	cases := map[string]struct {
		series *eventsv1.EventSeries
		count  int32
		last   metav1.Time
		want   *eventsv1.EventSeries
	}{
		"not repeating":      {},
		"counted once":       {count: 1},
		"counted negative":   {count: -1},
		"modern":             {series: modern, want: modern},
		"legacy":             {count: 7, last: testLegacyTime, want: &eventsv1.EventSeries{Count: 7, LastObservedTime: metav1.NewMicroTime(testLegacyTime.Time)}},
		"modern over legacy": {series: modern, count: 7, last: testLegacyTime, want: modern},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ev := lift(&eventsv1.Event{
				EventTime:               testEventTime,
				Series:                  tc.series,
				DeprecatedCount:         tc.count,
				DeprecatedLastTimestamp: tc.last,
			})
			if diff := cmp.Diff(tc.want, ev.Series); diff != "" {
				t.Errorf("the series differs (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLiftStampsReadTime asserts that an event carrying no time
// of its own is stamped with the time it was read at.
func TestLiftStampsReadTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ev := lift(&eventsv1.Event{})

		if want := time.Now(); !ev.EventTime.Time.Equal(want) {
			t.Errorf("got event time %s, want %s", ev.EventTime, want)
		}
		if ev.Time().IsZero() {
			t.Error("the occurrence time is zero, want the read time")
		}
	})
}

// testUpstreamEvent returns an event carrying every field the lift
// reads, written through the modern path.
func testUpstreamEvent() *eventsv1.Event {
	return &eventsv1.Event{
		Name:                "web.17f0",
		Namespace:           "team-a",
		UID:                 "d8f1",
		ResourceVersion:     "1042",
		Labels:              map[string]string{"app": "web"},
		Annotations:         map[string]string{"team": "hosting"},
		EventTime:           testEventTime,
		Series:              &eventsv1.EventSeries{Count: 4, LastObservedTime: testEventTime},
		ReportingController: "kubelet",
		ReportingInstance:   "node-001",
		Action:              "Killing",
		Reason:              "OOMKilled",
		Regarding:           corev1.ObjectReference{Kind: "Pod", Namespace: "team-a", Name: "web"},
		Related:             &corev1.ObjectReference{Kind: "Node", Name: "node-001"},
		Note:                "container exceeded its memory limit",
		Type:                "Warning",
	}
}
