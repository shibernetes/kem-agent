package sink

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/shibernetes/kem-agent/event"
)

// A Sink delivers events to a destination.
type Sink interface {
	// Name returns the sink's configured instance name.
	Name() string

	// Shutdown releases the sink's resources, bounded by the context.
	Shutdown(context.Context) error
}

// A BatchSink encodes events into frames and ships the composed bytes.
type BatchSink interface {
	Sink

	// Encoder returns the sink's encoder.
	Encoder() Encoder

	// Framer returns the sink's framer.
	Framer() Framer

	// Send delivers one composed payload, returning the number of
	// records the destination accepted the request for and then refused,
	// which is meaningful only alongside a nil error.
	//
	// A non-nil error means that the payload was not delivered and the
	// caller owns any retry. An error wrapping [ErrPermanent] signals that
	// no further retries can succeed, so the payload is dropped immediately.
	// An error wrapping [ErrOversized] indicates that the payload
	// exceeded the destination's size limit. Any other error is retried.
	//
	// Send is never called concurrently for a given sink.
	// Implementations must not retry internally, must not retain the
	// payload, and must return promptly once the context is done.
	Send(context.Context, []byte, BatchID) (int, error)
}

// An EventSink consumes events directly, having no wire format.
type EventSink interface {
	Sink

	// Handle records one event. It returns [ErrRejected] when the sink
	// declines the event, which is an accounting outcome rather than
	// a failure and must not be retried.
	Handle(*event.Event) error
}

// An Instrumented sink owns Prometheus collectors, which the agent
// registers when it starts.
type Instrumented interface {
	// Collectors returns the collectors the sink records into.
	Collectors() []prometheus.Collector
}

// An Opener sink holds a destination it acquires when the agent starts,
// rather than when it is built, since a factory performs no I/O.
type Opener interface {
	Open(context.Context) error
}

// A Framer composes frames into one request payload, and accounts for
// the bytes that composition adds. It runs on the sink's drainer alone,
// so unlike an [Encoder] it may hold mutable state.
type Framer interface {
	// Separator returns the bytes written between two frames.
	Separator() []byte

	// Compose wraps the accumulated frames into dst, and returns
	// the extended buffer.
	Compose(dst, frames []byte, n int) []byte

	// Fixed returns the total bytes the wrapping adds,
	// whatever the frame count is.
	Fixed() int
}

// An Encoder turns an event into a frame, appended into the buffer
// it is given and returned. It runs on every dispatch goroutine feeding
// the sink, so it holds no mutable state of its own.
type Encoder interface {
	AppendEvent([]byte, *event.Event) []byte
}
