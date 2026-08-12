package otel

import (
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	// fieldResourceLogs is the resource_logs field number in an export
	// request. Each frame represents one entry.
	fieldResourceLogs = 1
)

var (
	_ sink.Encoder = (*encoder)(nil)
	_ sink.Framer  = framer{}
)

// An encoder writes an event as one [logspb.ResourceLogs], with its
// field tag and varint length at the beginning.
type encoder struct {
	records sync.Pool
}

func newEncoder(meta identity.AgentMetadata) *encoder {
	return &encoder{
		records: sync.Pool{
			New: func() any {
				return newRecord(meta)
			},
		},
	}
}

// AppendEvent implements the [sink.Encoder] interface.
func (e *encoder) AppendEvent(dst []byte, ev *event.Event) []byte {
	rec, ok := e.records.Get().(*record)
	if !ok {
		return dst
	}
	defer e.records.Put(rec)

	rlogs := rec.fill(ev, time.Now())
	frame := protowire.AppendTag(dst, fieldResourceLogs, protowire.BytesType)

	frame = protowire.AppendVarint(frame, uint64(proto.Size(rlogs))) //nolint:gosec
	// DO NOT MUTATE rlogs PAST THIS POINT.
	//
	// MarshalOptions reuses the size measured for the length prefix,
	// which saves the marshal a second pass over the message.
	frame, err := proto.MarshalOptions{UseCachedSize: true}.MarshalAppend(frame, rlogs)
	if err != nil {
		// This can only fail on an invalid string, and a partial
		// frame would corrupt the batch, so it is dropped.
		return dst
	}
	return frame
}

// A framer joins frames into an export request.
// No envelope is needed since each frame already has a field tag and
// repeated fields can simply be concatenated.
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
