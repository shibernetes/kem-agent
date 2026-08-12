package otel

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
)

var observedAt = time.Date(2009, 1, 3, 18, 20, 1, 0, time.UTC)

func TestRecordSeverity(t *testing.T) {
	cases := map[string]struct {
		typ  string
		want logspb.SeverityNumber
	}{
		"normal":   {"Normal", logspb.SeverityNumber_SEVERITY_NUMBER_INFO},
		"warning":  {"Warning", logspb.SeverityNumber_SEVERITY_NUMBER_WARN},
		"error":    {"error", logspb.SeverityNumber_SEVERITY_NUMBER_ERROR},
		"critical": {"CRITICAL", logspb.SeverityNumber_SEVERITY_NUMBER_FATAL},
		"unmapped": {"audit", logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED},
		"unset":    {"", logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ev := testEvent()
			ev.Type = tc.typ

			entry := fillEntry(t, ev)
			if got := entry.GetSeverityNumber(); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
			if got := entry.GetSeverityText(); got != tc.typ {
				t.Errorf("got severity text %q, want %q", got, tc.typ)
			}
		})
	}
}

// TestRecordTimestamps asserts that a repeating event is stamped with
// its last observation, and every event with the time it was read.
func TestRecordTimestamps(t *testing.T) {
	cases := map[string]struct {
		event *event.Event
		want  time.Time
	}{
		"single":    {testEvent(), time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)},
		"repeating": {fullEvent(), time.Date(2009, 1, 3, 18, 20, 0, 0, time.UTC)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			entry := fillEntry(t, tc.event)

			if got := int64(entry.GetTimeUnixNano()); got != tc.want.UnixNano() {
				t.Errorf("got time %d, want %d", got, tc.want.UnixNano())
			}
			if got := int64(entry.GetObservedTimeUnixNano()); got != observedAt.UnixNano() {
				t.Errorf("got observed time %d, want %d", got, observedAt.UnixNano())
			}
		})
	}
}

func TestRecordBody(t *testing.T) {
	ev := testEvent()

	if got := fillEntry(t, ev).GetBody().GetStringValue(); got != ev.Note {
		t.Errorf("got body %q, want %q", got, ev.Note)
	}
}

// TestRecordSeriesAttributes asserts that a repeating event reports
// how often it occurred and the time of its first observation.
func TestRecordSeriesAttributes(t *testing.T) {
	want := []string{
		"k8s.event.start_time=2009-01-03T18:15:05Z",
		"k8s.event.count=7",
	}
	got := prefixed(attrPairs(fillEntry(t, fullEvent()).GetAttributes()), "k8s.event.start_time", "k8s.event.count")

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the attributes differ (-want +got):\n%s", diff)
	}
}

func TestRecordOmitsSeriesAttributes(t *testing.T) {
	cases := map[string]*event.Event{
		"no series":  testEvent(),
		"zero count": seriesEvent(0),
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			got := prefixed(attrPairs(fillEntry(t, ev).GetAttributes()), "k8s.event.count")
			if len(got) != 0 {
				t.Errorf("got %v, want no count", got)
			}
		})
	}
}

func TestRecordEventAttributes(t *testing.T) {
	want := []string{
		"k8s.event.name=web-1.17f0a2b3c4d5e6f7",
		"k8s.event.uid=d1f5c2a0-1111-2222-3333-444455556666",
		"k8s.event.namespace=team-a",
		"k8s.event.reason=OOMKilled",
		"k8s.event.reporting_controller=kubelet",
		"k8s.event.reporting_instance=kubelet-node-001",
	}
	if diff := cmp.Diff(want, attrPairs(fillEntry(t, testEvent()).GetAttributes())); diff != "" {
		t.Errorf("the attributes differ (-want +got):\n%s", diff)
	}
}

// TestRecordResourceWithoutEnrichment asserts that the resource of
// an unenriched event has attributes about the agent and the object it regards.
func TestRecordResourceWithoutEnrichment(t *testing.T) {
	want := []string{
		"service.name=kem-agent",
		"service.version=0.0.1",
		"k8s.cluster.name=prod-eu-1",
		"k8s.namespace.name=kem-system",
		"k8s.pod.name=kem-agent-0",
		"k8s.node.name=node-001",
		"k8s.object.kind=Pod",
		"k8s.object.name=web-1",
		"k8s.object.namespace=team-a",
		"k8s.object.uid=9c8b7a6d-5e4f-3210-9876-543210fedcba",
		"k8s.object.api_version=v1",
	}
	got := attrPairs(fillRecord(t, testEvent()).GetResource().GetAttributes())

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the attributes differ (-want +got):\n%s", diff)
	}
}

// TestRecordResetsArena asserts that the storage for the attribute keys
// does not grow across events, since one record serves a whole stream.
func TestRecordResetsArena(t *testing.T) {
	var (
		r  = newRecord(testAgentMetadata())
		ev = fullEvent()
	)
	r.fill(ev, observedAt)
	want := len(r.arena)

	for range 100 {
		r.fill(ev, observedAt)
	}
	if got := len(r.arena); got != want {
		t.Errorf("got %d bytes of storage after 100 events, want %d", got, want)
	}
}

func TestRecordSortsMetadata(t *testing.T) {
	ev := bareObjectEvent()
	ev.RegardingObject.Labels = map[string]string{
		"tier": "front", "app": "web", "zone": "a", "env": "prod", "release": "v2",
	}
	ev.RegardingObject.Annotations = map[string]string{"team": "platform", "owner": "sre"}

	want := []string{
		"k8s.object.label.app=web",
		"k8s.object.label.env=prod",
		"k8s.object.label.release=v2",
		"k8s.object.label.tier=front",
		"k8s.object.label.zone=a",
		"k8s.object.annotation.owner=sre",
		"k8s.object.annotation.team=platform",
	}
	got := prefixed(attrPairs(fillRecord(t, ev).GetResource().GetAttributes()),
		prefixObjectLabel, prefixObjectAnnotation)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the attributes differ (-want +got):\n%s", diff)
	}
}

// TestRecordKeepsInternedKeys asserts that every attribute key is read back
// as it was written, including those the arena handed out before it grew.
func TestRecordKeepsInternedKeys(t *testing.T) {
	const count = 200

	ev := fullEvent()
	ev.RegardingObject.Labels = make(map[string]string, count)
	want := make([]string, count)

	for i := range count {
		key, value := fmt.Sprintf("key%03d", i), strconv.Itoa(i)
		ev.RegardingObject.Labels[key] = value
		want[i] = prefixObjectLabel + key + "=" + value
	}
	// The second event is encoded by reusing the arena capacity
	// without growing it.
	entries := decodeBatch(t, composeBatch(newTestSink(t, DefaultConfig()), ev, ev)).GetResourceLogs()

	for i, entry := range entries {
		got := prefixed(attrPairs(entry.GetResource().GetAttributes()), prefixObjectLabel)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the attributes of entry %d differ (-want +got):\n%s", i, diff)
		}
	}
}

func TestRecordOwnerAttributes(t *testing.T) {
	cases := map[string]struct {
		event *event.Event
		want  []string
	}{
		"controlled": {fullEvent(), []string{
			"k8s.object.terminating=true",
			"k8s.object.owner.kind=ReplicaSet",
			"k8s.object.owner.name=web-7d9f4c",
			"k8s.object.owner.namespace=team-a",
			"k8s.object.owner.uid=3c4d5e6f-7a8b-9c0d-1e2f-3a4b5c6d7e8f",
			"k8s.object.owner.api_version=apps/v1",
		}},
		"standalone": {bareObjectEvent(), nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := prefixed(attrPairs(fillRecord(t, tc.event).GetResource().GetAttributes()),
				keyObjectTerminating, "k8s.object.owner.")

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("the attributes differ (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAgentAttributesSkipsUnset(t *testing.T) {
	want := []string{"service.name=kem-agent", "k8s.cluster.name=prod-eu-1"}

	if diff := cmp.Diff(want, attrPairs(agentAttributes(identity.AgentMetadata{Cluster: "prod-eu-1"}))); diff != "" {
		t.Errorf("the attributes differ (-want +got):\n%s", diff)
	}
}

func TestRecordClearsPreviousEvent(t *testing.T) {
	r := newRecord(testAgentMetadata())
	r.fill(fullEvent(), observedAt)

	var (
		logs  = r.fill(testEvent(), observedAt)
		entry = logs.GetScopeLogs()[0].GetLogRecords()[0]
	)
	got := prefixed(attrPairs(entry.GetAttributes()), "k8s.event.count", "k8s.event.start_time")
	if len(got) != 0 {
		t.Errorf("got %v, want the series of the previous event gone", got)
	}
	got = prefixed(attrPairs(logs.GetResource().GetAttributes()), prefixObjectLabel)
	if len(got) != 0 {
		t.Errorf("got %v, want the labels of the previous event gone", got)
	}
}

// fillRecord returns the resource logs an event fills.
func fillRecord(t *testing.T, ev *event.Event) *logspb.ResourceLogs {
	t.Helper()

	return newRecord(testAgentMetadata()).fill(ev, observedAt)
}

// fillEntry returns the log record one event fills.
func fillEntry(t *testing.T, ev *event.Event) *logspb.LogRecord {
	t.Helper()

	return fillRecord(t, ev).GetScopeLogs()[0].GetLogRecords()[0]
}

// bareObjectEvent returns an event enriched with an object that has
// no metadata, no controller, and is not terminating.
func bareObjectEvent() *event.Event {
	ev := testEvent()
	ev.RegardingObject = &event.RegardingObject{}

	return ev
}

// seriesEvent returns a repeating event with the given count.
func seriesEvent(count int32) *event.Event {
	ev := testEvent()
	ev.Series = &eventsv1.EventSeries{
		Count:            count,
		LastObservedTime: metav1.NewMicroTime(observedAt),
	}
	return ev
}

// prefixed returns the pairs whose key starts with one of the prefixes.
func prefixed(pairs []string, prefixes ...string) []string {
	var out []string
	for _, pair := range pairs {
		for _, prefix := range prefixes {
			if strings.HasPrefix(pair, prefix) {
				out = append(out, pair)
				break
			}
		}
	}
	return out
}
