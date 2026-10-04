package otel

import (
	"bytes"
	"slices"
	"strconv"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/shibernetes/kem-agent/event"
)

func TestParseFrameRejectsOtherLayouts(t *testing.T) {
	cases := map[string]func(*logspb.ResourceLogs){
		"two scopes":            func(r *logspb.ResourceLogs) { r.ScopeLogs = append(r.ScopeLogs, r.ScopeLogs[0]) },
		"resource schema URL":   func(r *logspb.ResourceLogs) { r.SchemaUrl = "https://opentelemetry.io/schemas/1.39.0" },
		"scope logs schema URL": func(r *logspb.ResourceLogs) { r.ScopeLogs[0].SchemaUrl = "https://opentelemetry.io/schemas/1.39.0" },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			rlogs := testResourceLogs()
			fn(rlogs)

			if _, ok := parseFrame(marshalFrame(t, rlogs)); ok {
				t.Error("the frame was parsed, want it rejected")
			}
		})
	}
}

func TestComposeCompactsSharedResource(t *testing.T) {
	var (
		frames  = appendFrames(notedEvent("first"), fullEvent(), notedEvent("second"))
		payload = newFramer().Compose(nil, frames, 3)
		rlogs   = decodeBatch(t, payload).GetResourceLogs()
	)
	if len(rlogs) != 2 {
		t.Fatalf("got %d resources, want 2", len(rlogs))
	}
	if got := notesOf(rlogs[0]); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("got notes %q under the first resource, want [first second]", got)
	}
	if got := notesOf(rlogs[1]); len(got) != 1 {
		t.Errorf("got %d records under the second resource, want 1", len(got))
	}
	if len(payload) >= len(frames) {
		t.Errorf("got %d bytes, want fewer than the %d bytes of the frames", len(payload), len(frames))
	}
}

// TestComposeGroupsManyObjects asserts that records are grouped correctly
// when the compactor has resources for many objects.
func TestComposeGroupsManyObjects(t *testing.T) {
	const objects = 40

	var events []*event.Event

	for range 2 {
		for i := range objects {
			ev := testEvent()
			ev.Regarding.Name = strconv.Itoa(i)
			events = append(events, ev)
		}
	}
	rlogs := decodeBatch(t, newFramer().Compose(nil, appendFrames(events...), len(events))).GetResourceLogs()
	if len(rlogs) != objects {
		t.Fatalf("got %d resources, want %d", len(rlogs), objects)
	}
	for i, rl := range rlogs {
		if got := len(notesOf(rl)); got != 2 {
			t.Errorf("got %d records under resource %d, want 2", got, i)
		}
	}
}

// TestComposeKeepsUnparsedFrame asserts that a frame that doesn't match the
// structure the encoder writes is sent unchanged, beside the compacted ones.
func TestComposeKeepsUnparsedFrame(t *testing.T) {
	other := testResourceLogs()
	other.SchemaUrl = "https://opentelemetry.io/schemas/1.39.0"

	frames := appendFrames(notedEvent("first"))
	frames = append(frames, marshalFrame(t, other)...)
	frames = append(frames, appendFrames(notedEvent("second"))...)

	rlogs := decodeBatch(t, newFramer().Compose(nil, frames, 3)).GetResourceLogs()
	if len(rlogs) != 2 {
		t.Fatalf("got %d resources, want 2", len(rlogs))
	}
	if got := notesOf(rlogs[0]); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("got notes %q under the first resource, want [first second]", got)
	}
	if got := rlogs[1].GetSchemaUrl(); got != other.SchemaUrl {
		t.Errorf("got schema URL %q on the second resource, want %q", got, other.SchemaUrl)
	}
}

// TestComposeKeepsTrailingBytes asserts that an incomplete frame at the
// end of a batch is sent unchanged, after the compacted frames.
func TestComposeKeepsTrailingBytes(t *testing.T) {
	frame := appendFrames(testEvent())
	if len(frame) == 0 {
		t.Fatal("the encoder dropped the event")
	}
	var (
		trail = frame[:len(frame)-1]
		got   = newFramer().Compose(nil, slices.Concat(frame, trail), 2)
	)
	if want := slices.Concat(newFramer().Compose(nil, frame, 1), trail); !bytes.Equal(got, want) {
		t.Error("the cut frame wasn't appended after the compacted frames")
	}
}

// TestComposeStartsEachBatchAfresh asserts that a framer, which reuses
// its buffers, doesn't carry records over from one batch to the next.
func TestComposeStartsEachBatchAfresh(t *testing.T) {
	f := newFramer()
	f.Compose(nil, appendFrames(notedEvent("earlier")), 1)

	frames := appendFrames(notedEvent("later"))
	if got, want := f.Compose(nil, frames, 1), newFramer().Compose(nil, frames, 1); !bytes.Equal(got, want) {
		t.Error("batch includes records from the previous one")
	}
}

// TestCompactKeepsNoReferenceToBatch asserts that a compactor no longer
// points into a batch once it's composed, so the drainer can release its
// accumulation buffer.
func TestCompactKeepsNoReferenceToBatch(t *testing.T) {
	c := newCompactor()
	c.compact(nil, appendFrames(testEvent(), fullEvent()))

	for _, g := range c.groups {
		if g.resource != nil || g.scope != nil || g.frame != nil {
			t.Fatal("group still points into the batch")
		}
	}
	for _, r := range c.records {
		if r.records != nil {
			t.Fatal("record still points into the batch")
		}
	}
}

// testResourceLogs returns a ResourceLogs with the structure the encoder writes.
func testResourceLogs() *logspb.ResourceLogs {
	return &logspb.ResourceLogs{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{{
				Key:   "k8s.object.kind",
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "Pod"}},
			}},
		},
		ScopeLogs: []*logspb.ScopeLogs{{
			Scope:      &commonpb.InstrumentationScope{Name: scopeName},
			LogRecords: []*logspb.LogRecord{{SeverityText: "Warning"}},
		}},
	}
}

// marshalFrame encodes rlogs as a frame, field tag and length included.
func marshalFrame(t *testing.T, rlogs *logspb.ResourceLogs) []byte {
	t.Helper()

	b, err := proto.Marshal(rlogs)
	if err != nil {
		t.Fatalf("failed to marshal resource logs: %v", err)
	}
	frame := protowire.AppendTag(nil, fieldResourceLogs, protowire.BytesType)

	return protowire.AppendBytes(frame, b)
}

// appendFrames returns the frames the encoder writes for the given
// events, joined end to end.
func appendFrames(events ...*event.Event) []byte {
	var (
		enc    = newEncoder(testAgentMetadata())
		frames []byte
	)
	for _, ev := range events {
		frames = enc.AppendEvent(frames, ev)
	}
	return frames
}

// notedEvent returns an event with the given note, which becomes
// the body of its record.
func notedEvent(note string) *event.Event {
	ev := testEvent()
	ev.Note = note

	return ev
}

// notesOf returns the body of each record of rlogs, in order.
func notesOf(rlogs *logspb.ResourceLogs) []string {
	var notes []string

	for _, slogs := range rlogs.GetScopeLogs() {
		for _, rec := range slogs.GetLogRecords() {
			notes = append(notes, rec.GetBody().GetStringValue())
		}
	}
	return notes
}
