package stdout

import (
	"context"

	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/shared"
)

var _ sink.BatchSink = (*Sink)(nil)

// A Sink writes each event as one JSON line to standard output.
type Sink struct {
	name    string
	writer  *shared.Writer
	encoder sink.Encoder
	framer  sink.Framer
}

// New returns a sink writing JSON lines to standard output.
func New(name string) *Sink {
	return &Sink{
		name:    name,
		writer:  shared.Stdout(),
		encoder: shared.NewJSONEncoder(),
		framer:  shared.NewJSONFramer(),
	}
}

// Name implements the [sink.Sink] interface.
func (s *Sink) Name() string {
	return s.name
}

// Encoder implements the [sink.BatchSink] interface.
func (s *Sink) Encoder() sink.Encoder {
	return s.encoder
}

// Framer implements the [sink.BatchSink] interface.
func (s *Sink) Framer() sink.Framer {
	return s.framer
}

// Send implements the [sink.BatchSink] interface.
// It writes synchronously and cannot be interrupted, so the sink
// has no send timeout and never reports one. The refused count is
// always zero.
func (s *Sink) Send(_ context.Context, payload []byte, _ sink.BatchID) (int, error) {
	return 0, s.writer.Write(payload)
}

// Shutdown implements the [sink.Sink] interface.
// Standard output belongs to the process and is never closed.
func (s *Sink) Shutdown(context.Context) error {
	return s.writer.Close()
}
