package otel

import (
	"bytes"
	"slices"
	"testing"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/shibernetes/kem-agent/event"
)

func TestAppendEventFrame(t *testing.T) {
	frame := newEncoder(testAgentMetadata()).AppendEvent(nil, testEvent())
	if len(frame) == 0 {
		t.Fatal("the encoder returned no frame")
	}
	num, typ, tag := protowire.ConsumeTag(frame)
	if tag < 0 {
		t.Fatalf("failed to read tag: %v", protowire.ParseError(tag))
	}
	if num != fieldResourceLogs {
		t.Errorf("got field number %d, want %d", num, fieldResourceLogs)
	}
	if typ != protowire.BytesType {
		t.Errorf("got wire type %d, want %d", typ, protowire.BytesType)
	}
	entry, size := protowire.ConsumeBytes(frame[tag:])
	if size < 0 {
		t.Fatalf("failed to read entry: %v", protowire.ParseError(size))
	}
	if got := tag + size; got != len(frame) {
		t.Errorf("got %d bytes of frame, want all %d", got, len(frame))
	}
	var logs logspb.ResourceLogs

	if err := proto.Unmarshal(entry, &logs); err != nil {
		t.Fatalf("failed to decode entry: %v", err)
	}
	if got := len(logs.GetScopeLogs()); got != 1 {
		t.Errorf("got %d scopes, want 1", got)
	}
}

// TestAppendEventConcatenates asserts that frames written to one buffer
// read back as an export request, in the order they were encoded.
func TestAppendEventConcatenates(t *testing.T) {
	var (
		enc   = newEncoder(testAgentMetadata())
		first = enc.AppendEvent(nil, testEvent())
		both  = enc.AppendEvent(slices.Clone(first), fullEvent())
	)
	if !bytes.HasPrefix(both, first) {
		t.Error("the first frame was overwritten, want it kept")
	}
	entries := decodeBatch(t, both).GetResourceLogs()
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	for i, want := range []string{
		"",
		"k8s.event.count=7",
	} {
		got := prefixed(attrPairs(entryOf(entries[i]).GetAttributes()), "k8s.event.count")
		if want == "" && len(got) != 0 {
			t.Errorf("got %v at entry %d, want the single event", got, i)
		}
		if want != "" && (len(got) != 1 || got[0] != want) {
			t.Errorf("got %v at entry %d, want %q", got, i, want)
		}
	}
}

// TestAppendEventSetsObservedTime asserts that the observed time of an
// event is the moment it was encoded.
func TestAppendEventSetsObservedTime(t *testing.T) {
	before := time.Now()
	frame := newEncoder(testAgentMetadata()).AppendEvent(nil, testEvent())
	after := time.Now()

	entry := entryOf(decodeBatch(t, frame).GetResourceLogs()[0])
	got := time.Unix(0, int64(entry.GetObservedTimeUnixNano()))

	if got.Before(before) || got.After(after) {
		t.Errorf("got %s, want time between %s and %s", got, before, after)
	}
}

// TestAppendEventDropsUnencodableEvent asserts that an event that cannot
// be marshaled leaves the buffer as it was. Otherwise, a partial frame
// would corrupt every appended frame after it.
func TestAppendEventDropsUnencodableEvent(t *testing.T) {
	var (
		enc = newEncoder(testAgentMetadata())
		buf = enc.AppendEvent(nil, testEvent())
	)
	ev := testEvent()
	ev.Note = "an invalid \xff rune"

	if got := enc.AppendEvent(buf, ev); len(got) != len(buf) {
		t.Errorf("got %d bytes, want %d", len(got), len(buf))
	}
}

// TestFramerAppendsFrames asserts that composing a batch adds no envelope of its own.
func TestFramerAppendsFrames(t *testing.T) {
	frames := []byte("two-frames")

	got := framer{}.Compose([]byte("head"), frames, 2)
	if string(got) != "head"+string(frames) {
		t.Errorf("got %q, want the frames appended to the buffer", got)
	}
}

// TestFramerSizeIsExact asserts that a composed payload measures exactly
// what the drainer computes from the envelope, the frame lengths and the
// separators.
func TestFramerSizeIsExact(t *testing.T) {
	var (
		enc    = newEncoder(testAgentMetadata())
		f      = framer{}
		events = []*event.Event{testEvent(), fullEvent(), testEvent()}
		frames []byte
		sum    int
	)
	for _, ev := range events {
		n := len(frames)
		frames = enc.AppendEvent(frames, ev)
		sum += len(frames) - n
	}
	want := f.Fixed() + sum + (len(events)-1)*len(f.Separator())

	if got := len(f.Compose(nil, frames, len(events))); got != want {
		t.Errorf("got %d bytes, want %d", got, want)
	}
}

// entryOf returns the log record of one export request entry.
func entryOf(logs *logspb.ResourceLogs) *logspb.LogRecord {
	return logs.GetScopeLogs()[0].GetLogRecords()[0]
}
