package otel

import (
	"fmt"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/mem"
	"google.golang.org/protobuf/proto"

	"github.com/shibernetes/kem-agent/sink"
)

const (
	// codecName is the content subtype the request is sent under.
	codecName = "proto"
)

var _ encoding.CodecV2 = rawCodec{}

// A rawCodec sends a payload that is already encoded. The sink composes
// the request itself, so marshaling hands over the bytes directly.
type rawCodec struct{}

// Name implements the [encoding.CodecV2] interface.
func (rawCodec) Name() string {
	return codecName
}

// Marshal implements the [encoding.CodecV2] interface.
func (rawCodec) Marshal(v any) (mem.BufferSlice, error) {
	b, ok := v.([]byte)
	if !ok {
		return nil, fmt.Errorf("%w: sink/otel: cannot send a %T", sink.ErrPermanent, v)
	}
	return mem.BufferSlice{mem.SliceBuffer(b)}, nil
}

// Unmarshal implements the [encoding.CodecV2] interface.
func (rawCodec) Unmarshal(data mem.BufferSlice, v any) error {
	msg, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("%w: sink/otel: cannot read a %T", sink.ErrPermanent, v)
	}
	return proto.Unmarshal(data.Materialize(), msg)
}

// refusedRecords returns how many records the destination refused after
// accepting the request. The count is not checked here, since the drainer
// bounds it against the batch.
func refusedRecords(resp *collogs.ExportLogsServiceResponse) int {
	return int(resp.GetPartialSuccess().GetRejectedLogRecords())
}
