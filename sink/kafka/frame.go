package kafka

import (
	"bytes"
	"encoding/binary"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/shared"
)

const (
	// lengthSize is the width of the length that precedes the key and the
	// value of a frame.
	lengthSize = 4
)

var (
	_ sink.Encoder = encoder{}
	_ sink.Framer  = framer{}
)

// An encoder writes an event as one frame, which contains the key and the
// value of a record, each preceded by its length as a 4-byte big-endian
// integer. The value is the canonical JSON form of the event.
type encoder struct {
	key MessageKey
}

// AppendEvent implements the [sink.Encoder] interface.
func (e encoder) AppendEvent(dst []byte, ev *event.Event) []byte {
	key := e.keyOf(ev)
	frame := binary.BigEndian.AppendUint32(dst, uint32(len(key))) //nolint:gosec
	frame = append(frame, key...)

	// The length of the value is only known after marshaling,
	// so its four bytes are reserved and filled in afterwards.
	at := len(frame)
	frame = binary.BigEndian.AppendUint32(frame, 0)

	value := shared.AppendJSON(frame, ev.MarshalJSONTo)
	if len(value) == len(frame) {
		return dst
	}
	// The JSON encoder ends a value with a newline, which a record doesn't need.
	value = bytes.TrimSuffix(value, []byte("\n"))
	binary.BigEndian.PutUint32(value[at:], uint32(len(value)-at-lengthSize)) //nolint:gosec

	return value
}

// keyOf returns the field of ev that is written as the key of its record.
func (e encoder) keyOf(ev *event.Event) string {
	switch e.key {
	case MessageKeyUID:
		return string(ev.UID)
	case MessageKeyNamespace:
		return ev.Namespace
	}
	return ""
}

// A framer joins frames end to end, since each frame carries its own lengths.
type framer struct{}

// Separator implements the [sink.Framer] interface.
func (framer) Separator() []byte {
	return nil
}

// Compose implements the [sink.Framer] interface.
func (framer) Compose(dst, frames []byte, _ int) []byte {
	return append(dst, frames...)
}

// Fixed implements the [sink.Framer] interface.
func (framer) Fixed() int {
	return 0
}

// readFrame reads the first frame of a composed payload. It returns the
// key and the value of that frame, followed by the frames that come after
// it. It reports false when the payload ends in the middle of a frame.
func readFrame(payload []byte) ([]byte, []byte, []byte, bool) {
	key, rest, ok := cutBytes(payload)
	if !ok {
		return nil, nil, payload, false
	}
	value, rest, ok := cutBytes(rest)
	if !ok {
		return nil, nil, payload, false
	}
	return key, value, rest, true
}

// cutBytes returns the length-prefixed byte string at the start of b,
// or nil when its length is zero, along with the bytes that follow it.
func cutBytes(b []byte) ([]byte, []byte, bool) {
	if len(b) < lengthSize {
		return nil, b, false
	}
	n := binary.BigEndian.Uint32(b)
	b = b[lengthSize:]

	if uint64(n) > uint64(len(b)) {
		return nil, b, false
	}
	if n == 0 {
		return nil, b, true
	}
	return b[:n], b[n:], true
}
