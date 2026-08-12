package shared

import (
	"bytes"
	"encoding/json/jsontext"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/shibernetes/kem-agent/event"
)

// TestAppendEventWritesOneLine asserts that a frame is a whole JSON
// object followed by a single newline, which is what makes the frames
// concatenable without a separator.
func TestAppendEventWritesOneLine(t *testing.T) {
	cases := map[string]*event.Event{
		"empty":  {},
		"filled": {Type: "Warning", Reason: "OOMKilled", Note: "container killed"},
		"labels": {Labels: map[string]string{"app": "web"}},
		"large":  {Note: strings.Repeat("x", 1<<20)},
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			frame := encode(t, ev)

			if n := bytes.Count(frame, []byte("\n")); n != 1 {
				t.Errorf("got %d newlines, want 1", n)
			}
			if frame[0] != '{' {
				t.Errorf("the frame opens with %q, want an object", frame[:1])
			}
			if !bytes.HasSuffix(frame, []byte("}\n")) {
				t.Errorf("the frame ends with %q, want a closed object and a newline", frame[len(frame)-2:])
			}
		})
	}
}

func TestAppendEventKeepsBuffer(t *testing.T) {
	buf := NewJSONEncoder().AppendEvent([]byte("kept\n"), &event.Event{Type: "Warning"})

	if !bytes.HasPrefix(buf, []byte("kept\n")) {
		t.Errorf("got %q, want the buffer it was given left in place", buf)
	}
	if !bytes.HasSuffix(buf, []byte("}\n")) {
		t.Errorf("got %q, want the frame appended after it", buf)
	}
}

// TestAppendEventManglesInvalidUTF8 asserts that a string the encoder
// cannot represent is written as replacement characters rather than
// refused. A refusal yields an empty frame, which loses the event with
// nothing to count it.
func TestAppendEventManglesInvalidUTF8(t *testing.T) {
	// A lone surrogate half, which is not valid UTF-8.
	frame := encode(t, &event.Event{Reason: "OOM\xed\xa0\x80"})

	if !bytes.Contains(frame, []byte("�")) {
		t.Errorf("got %q, want the invalid bytes replaced", frame)
	}
}

// TestAppendEventConcurrent asserts that encoders reached from several
// goroutines produce whole frames.
func TestAppendEventConcurrent(t *testing.T) {
	enc := NewJSONEncoder()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			var (
				idx  = strconv.Itoa(i)
				want = []byte(`"reason":"` + idx + `"`)
				buf  []byte
			)
			for range 100 {
				buf = enc.AppendEvent(buf[:0], &event.Event{Reason: idx})
				if !bytes.Contains(buf, want) {
					t.Errorf("got %q, want it to carry %q", buf, want)
					return
				}
			}
		})
	}
	wg.Wait()
}

// TestAppendEventReleasesBuffer asserts that the encoder does not keep
// the caller's buffer, which would pin it until the next event.
func TestAppendEventReleasesBuffer(t *testing.T) {
	var (
		used *jsonBuffer
		prev = buffers
	)
	buffers = &sync.Pool{New: func() any {
		b := &jsonBuffer{}
		b.enc = jsontext.NewEncoder(b)
		used = b
		return b
	}}
	t.Cleanup(func() {
		buffers = prev
	})
	NewJSONEncoder().AppendEvent(make([]byte, 0, 512), &event.Event{Type: "Warning"})

	if used == nil {
		t.Fatal("encode took no buffer from the pool, want one")
	}
	if used.buf != nil {
		t.Errorf("the pooled encoder holds %d bytes, want none", cap(used.buf))
	}
}

func TestFramerAddsNothing(t *testing.T) {
	f := NewJSONFramer()

	if got := f.Separator(); got != nil {
		t.Errorf("got separator %q, want none", got)
	}
	if got := f.Fixed(); got != 0 {
		t.Errorf("got %d fixed bytes, want 0", got)
	}
}

func TestFramerComposeAppends(t *testing.T) {
	frames := []byte("{\"a\":1}\n{\"b\":2}\n")

	got := NewJSONFramer().Compose([]byte("kept\n"), frames, 2)
	if want := append([]byte("kept\n"), frames...); !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// encode returns the frame of an event, failing when it is empty.
func encode(t *testing.T, ev *event.Event) []byte {
	t.Helper()

	frame := NewJSONEncoder().AppendEvent(nil, ev)
	if len(frame) == 0 {
		t.Fatal("event encoded to nothing, want a frame")
	}
	return frame
}
