package file

import (
	"context"

	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/shared"
)

var (
	_ sink.BatchSink = (*Sink)(nil)
	_ sink.Opener    = (*Sink)(nil)
)

// A Sink writes each event as one JSON line to a file.
type Sink struct {
	name    string
	writer  *shared.Writer
	encoder sink.Encoder
	framer  sink.Framer
}

// New returns a sink writing JSON lines to a file. The file is
// opened lazily by Open, so that building the sink performs no I/O.
func New(name string, cfg Config) *Sink {
	return &Sink{
		name:    name,
		writer:  shared.NewWriter(cfg.Path, cfg.Mode),
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

// Open implements the [sink.Opener] interface.
// It runs at start and again on every rotation of the file.
func (s *Sink) Open(context.Context) error {
	return s.writer.Open()
}

// Send implements the [sink.BatchSink] interface.
// It writes synchronously and cannot be interrupted, so the sink has no
// send timeout and never reports one. The refused count is always zero.
func (s *Sink) Send(_ context.Context, payload []byte, _ sink.BatchID) (int, error) {
	return 0, s.writer.Write(payload)
}

// Shutdown implements the [sink.Sink] interface.
// It closes the underlying file held by the writer.
func (s *Sink) Shutdown(context.Context) error {
	return s.writer.Close()
}
