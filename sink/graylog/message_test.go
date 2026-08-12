package graylog

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
)

var gelfFieldRe = regexp.MustCompile(`^_[\w.\-]*$`)

func TestAppendEventWritesOneMessage(t *testing.T) {
	frame := newEncoder(testConfig(), agentMetadata()).AppendEvent(nil, testEvent())

	if n := strings.Count(string(frame), "\x00"); n != 1 {
		t.Errorf("got %d terminators, want 1", n)
	}
	if frame[len(frame)-1] != 0 {
		t.Error("the message does not end with its terminator")
	}
	if !jsontext.Value(frame[:len(frame)-1]).IsValid() {
		t.Errorf("the message is not valid JSON: %s", frame)
	}
}

func TestAppendEventKeepsBuffer(t *testing.T) {
	frame := newEncoder(testConfig(), agentMetadata()).AppendEvent([]byte("kept\x00"), testEvent())

	if !strings.HasPrefix(string(frame), "kept\x00{") {
		t.Errorf("got %q, want the buffer it was given left in place", frame[:16])
	}
}

// TestShortMessageFallsBack asserts that an event with no reason
// still reports a short message, which GELF requires.
func TestShortMessageFallsBack(t *testing.T) {
	cases := map[string]struct {
		reason string
		want   string
	}{
		"with":    {"OOMKilled", `"short_message":"OOMKilled"`},
		"without": {"", `"short_message":"-"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assertContains(t, encode(t, &event.Event{Reason: tc.reason}), tc.want)
		})
	}
}

func TestFullMessage(t *testing.T) {
	const note = "It's a trap!"

	assertOmits(t, encode(t, &event.Event{}), "full_message")
	assertContains(t, encode(t, &event.Event{Note: note}), `"full_message":`+strconv.Quote(note))
}

func TestLevel(t *testing.T) {
	cases := map[string]string{
		"Warning": `"level":4`,
		"Normal":  `"level":6`,
		"Unknown": `"level":6`,
		"":        `"level":6`,
	}
	for eventType, want := range cases {
		t.Run(eventType, func(t *testing.T) {
			assertContains(t, encode(t, &event.Event{Type: eventType}), want)
		})
	}
}

func TestTimestampOmittedForZeroTime(t *testing.T) {
	assertOmits(t, encode(t, &event.Event{}), "timestamp")
}

// TestSeries asserts that a repeating event reports its count and the
// time it was first seen at, the message timestamp being the last one.
func TestSeries(t *testing.T) {
	at := time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)
	ev := &event.Event{
		EventTime: metav1.NewMicroTime(at),
		Series: &eventsv1.EventSeries{
			Count:            7,
			LastObservedTime: metav1.NewMicroTime(at.Add(4 * time.Minute)),
		},
	}
	got := encode(t, ev)

	assertContains(t, got, `"_event_count":7`)
	assertContains(t, got, `"_event_start_time":"2009-01-03T18:15:05Z"`)
	assertContains(t, got, `"timestamp":1231006745.000`) // 4 minutes after
}

// TestSeriesAbsent asserts that an event seen once still reports a count,
// and no first occurrence, which would otherwise repeat the message timestamp.
func TestSeriesAbsent(t *testing.T) {
	got := encode(t, &event.Event{})

	assertContains(t, got, `"_event_count":1`)
	assertOmits(t, got, "_event_start_time")
}

// TestRegardingObjectAbsent asserts that an event that was not enriched
// has none of the regarding object fields.
func TestRegardingObjectAbsent(t *testing.T) {
	got := encode(t, &event.Event{})

	assertOmits(t, got, "_object_owner_kind")
	assertOmits(t, got, "_object_terminating")
}

func TestRegardingObjectWithoutOwner(t *testing.T) {
	got := encode(t, &event.Event{RegardingObject: &event.RegardingObject{}})

	assertContains(t, got, `"_object_terminating":false`)
	assertOmits(t, got, "_object_owner_kind")
}

func TestEmptyFieldsAreOmitted(t *testing.T) {
	got := encodeWith(t, testConfig(), identity.AgentMetadata{}, &event.Event{})

	for _, name := range []string{
		"_event_reason",
		"_event_action",
		"_event_type",
		"_event_name",
		"_namespace_name",
		"_object_kind",
		"_kem_cluster",
		"_kem_version",
	} {
		assertOmits(t, got, name)
	}
}

// TestFieldNamesAreValid asserts that every additional field a message
// carries are valid according to the GELF spec.
// https://go2docs.graylog.org/current/getting_in_log_data/gelf_format.html#GELFPayloadSpecification
func TestFieldNamesAreValid(t *testing.T) {
	cfg := testConfig()
	cfg.AdditionalFields = map[string]string{"app.kubernetes.io/part-of": "platform"}

	ev := testEvent()
	ev.RegardingObject.Annotations = map[string]string{"cni.projectcalico.org/podIP": "10.1.2.3"}

	var additional int
	for name := range decode(t, encodeWith(t, cfg, agentMetadata(), ev)) {
		if !strings.HasPrefix(name, "_") {
			continue
		}
		additional++

		if !gelfFieldRe.MatchString(name) {
			t.Errorf("%q is not a valid GELF field name", name)
		}
	}
	// A message with no additional fields would pass the loop
	// without asserting anything.
	if additional == 0 {
		t.Error("the message has no additional field, want 2")
	}
}

// TestFieldNamesAreNormalized asserts that a label or annotation key with a
// qualified name separator is normalized as a valid GELF field name.
func TestFieldNamesAreNormalized(t *testing.T) {
	cfg := testConfig()
	cfg.AdditionalFields = map[string]string{"example.com/tenant": "acme"}

	ev := &event.Event{RegardingObject: &event.RegardingObject{
		Labels:      map[string]string{"helm.sh/chart": "web-1.2.3"},
		Annotations: map[string]string{"prometheus.io/scrape": "true"},
	}}
	got := encodeWith(t, cfg, agentMetadata(), ev)

	assertContains(t, got, `"_example.com_tenant":"acme"`)
	assertContains(t, got, `"_object_label_helm.sh_chart":"web-1.2.3"`)
	assertContains(t, got, `"_object_annotation_prometheus.io_scrape":"true"`)
}

// TestMapFieldsAreSorted asserts that the keys of a map reach are
// written sorted, in order to produce a stable event format.
// The object metadata and the configured additional fields are sorted
// separately, so both are covered in the test.
func TestMapFieldsAreSorted(t *testing.T) {
	entries := map[string]string{"e": "5", "d": "4", "c": "3", "b": "2", "a": "1"}

	cfg := testConfig()
	cfg.AdditionalFields = entries

	ev := &event.Event{
		RegardingObject: &event.RegardingObject{Labels: entries},
	}
	got := encodeWith(t, cfg, agentMetadata(), ev)

	assertContains(t, got, `"_object_label_a":"1","_object_label_b":"2","_object_label_c":"3","_object_label_d":"4","_object_label_e":"5"`)
	assertContains(t, got, `"_a":"1","_b":"2","_c":"3","_d":"4","_e":"5"`)
}

// TestInvalidUTF8IsReplaced asserts that a value the encoder
// cannot represent is mangled rather than refused.
func TestInvalidUTF8IsReplaced(t *testing.T) {
	got := encode(t, &event.Event{Reason: "OOM\xed\xa0\x80"})

	assertContains(t, got, "�")
	if !jsontext.Value(got).IsValid() {
		t.Errorf("the message is not valid JSON: %s", got)
	}
}

func TestFramer(t *testing.T) {
	f := newFramer()

	if got := f.Separator(); got != nil {
		t.Errorf("got separator %q, want none", got)
	}
	if got := f.Fixed(); got != 0 {
		t.Errorf("got %d fixed bytes, want 0", got)
	}
	if got := string(f.Compose([]byte("foo"), []byte("bar"), 2)); got != "foobar" {
		t.Errorf("got %q, want %q", got, "foobar")
	}
}

// encode returns the encoded form of an event without terminator.
func encode(t *testing.T, ev *event.Event) string {
	t.Helper()

	return encodeWith(t, testConfig(), agentMetadata(), ev)
}

func encodeWith(t *testing.T, cfg Config, id identity.AgentMetadata, ev *event.Event) string {
	t.Helper()

	frame := newEncoder(cfg, id).AppendEvent(nil, ev)
	if len(frame) == 0 || frame[len(frame)-1] != 0 {
		t.Fatalf("the message has no terminator: %q", frame)
	}
	return string(frame[:len(frame)-1])
}

func decode(t *testing.T, message string) map[string]jsontext.Value {
	t.Helper()

	var m map[string]jsontext.Value
	if err := jsonv2.Unmarshal([]byte(message), &m); err != nil {
		t.Fatalf("failed to decode message: %v", err)
	}
	return m
}

func assertContains(t *testing.T, message, want string) {
	t.Helper()

	if !strings.Contains(message, want) {
		t.Errorf("the message doesn't contain %s, want it included", want)
		t.Logf("%s", message)
	}
}

func assertOmits(t *testing.T, message, name string) {
	t.Helper()

	if strings.Contains(message, name) {
		t.Errorf("the message contains %s, want it omitted", name)
		t.Logf("%s", message)
	}
}

func agentMetadata() identity.AgentMetadata {
	return identity.AgentMetadata{
		Cluster:   "prod-eu-1",
		Node:      "node-007",
		Namespace: "kem-system",
		Pod:       "kem-agent-7c9d4f-x8m2p",
		Version:   "v0.0.0",
	}
}

func testEvent() *event.Event {
	at := time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)

	return &event.Event{
		UID:             "8f1d2c4a-3b5e-4c7d-9a1f-2e6b8c0d4f7a",
		ResourceVersion: "4021",
		Namespace:       "team-a",
		Name:            "web-7d9f4-x2k1.17ab3c9e2d1f",
		EventTime:       metav1.NewMicroTime(at),
		Series: &eventsv1.EventSeries{
			Count:            7,
			LastObservedTime: metav1.NewMicroTime(at.Add(4 * time.Minute)),
		},
		ReportingController: "kubelet",
		ReportingInstance:   "node-007",
		Action:              "Killing",
		Reason:              "OOMKilled",
		Regarding: corev1.ObjectReference{
			Kind:            "Pod",
			Namespace:       "team-a",
			Name:            "web-7d9f4-x2k1",
			UID:             "1a2b3c4d-5e6f-4a8b-9c0d-1e2f3a4b5c6d",
			APIVersion:      "v1",
			ResourceVersion: "3987",
		},
		Note: `Container "web" exceeded its memory limit of 512Mi`,
		Type: "Warning",
		RegardingObject: &event.RegardingObject{
			Labels: map[string]string{
				"app.kubernetes.io/name": "web",
				"tier":                   "frontend",
			},
			Owner: &corev1.ObjectReference{
				Kind: "ReplicaSet",
				Name: "web-7d9f4",
			},
		},
	}
}
