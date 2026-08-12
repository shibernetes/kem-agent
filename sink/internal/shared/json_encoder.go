package shared

import (
	"encoding/json/jsontext"
	"sync"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/sink"
)

// buffers pools the JSON encoders which write frames.
var buffers = &sync.Pool{
	New: func() any {
		b := &jsonBuffer{}
		b.enc = jsontext.NewEncoder(b)
		return b
	},
}

var (
	_ sink.Encoder = jsonEncoder{}
	_ sink.Framer  = jsonFramer{}
)

// A jsonEncoder writes an event as one JSON line.
type jsonEncoder struct{}

// NewJSONEncoder returns an encoder writing one JSON line per event.
func NewJSONEncoder() sink.Encoder {
	return jsonEncoder{}
}

// AppendEvent implements the [sink.Encoder] interface.
func (jsonEncoder) AppendEvent(dst []byte, ev *event.Event) []byte {
	b := buffers.Get().(*jsonBuffer)
	b.buf = dst

	// Reset replaces the options given at construction, so they are set here.
	// Invalid UTF-8 is mangled to U+FFFD rather than refused, which would yield
	// an empty frame and lose the event with nothing counting it.
	// Duplicate names are left unchecked because the encoder does not produce one.
	b.enc.Reset(b, jsontext.AllowInvalidUTF8(true), jsontext.AllowDuplicateNames(true))

	// The encoder terminates every top-level value with a newline,
	// which is the line framing, so none is appended here.
	err := ev.MarshalJSONTo(b.enc)
	out := b.buf
	b.buf = nil
	buffers.Put(b)

	if err != nil {
		// Tokens are written to memory, so a failure is a malformed
		// sequence rather than an I/O error. What was written is
		// discarded, since a partial line corrupts the stream.
		return dst
	}
	return out
}

// AppendJSON appends the JSON value written by fn to dst, and returns
// the extended buffer. The encoder and its buffer are pooled, so a
// caller writing one value per event allocates neither. A write error
// discards what was written, since a partial value corrupts the frame.
func AppendJSON(dst []byte, fn func(*jsontext.Encoder) error) []byte {
	b := buffers.Get().(*jsonBuffer)
	b.buf = dst

	// Reset replaces the options given at construction.
	// Invalid UTF-8 is mangled to U+FFFD rather than refused, which would yield
	// an empty frame and drop the event without counting it, and duplicate names
	// go unchecked for the reason given in AppendEvent.
	b.enc.Reset(b, jsontext.AllowInvalidUTF8(true), jsontext.AllowDuplicateNames(true))

	err := fn(b.enc)
	out := b.buf
	b.buf = nil
	buffers.Put(b)

	if err != nil {
		return dst
	}
	return out
}

// jsonBuffer is the byte slice a JSON encoder writes to, pooled
// together with the encoder so that neither escapes per event.
type jsonBuffer struct {
	buf []byte
	enc *jsontext.Encoder
}

// Write implements the [io.Writer] interface.
func (b *jsonBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// A jsonFramer joins frames that are already whole lines.
type jsonFramer struct{}

// NewJSONFramer returns a framer concatenating line-terminated frames.
func NewJSONFramer() sink.Framer {
	return jsonFramer{}
}

// Separator implements the [sink.Framer] interface.
func (jsonFramer) Separator() []byte {
	return nil
}

// Compose implements the [sink.Framer] interface.
func (jsonFramer) Compose(dst, frames []byte, _ int) []byte {
	return append(dst, frames...)
}

// Fixed implements the [sink.Framer] interface.
func (jsonFramer) Fixed() int {
	return 0
}
