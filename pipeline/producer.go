package pipeline

import (
	"context"
	"log/slog"
	"time"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/buffer"
	"github.com/shibernetes/kem-agent/internal/logsample"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	// bufSizeThreshold is the capacity in bytes above which
	// a producer releases the buffer it encodes into.
	bufSizeThreshold = 64 << 10 // 64 KiB

	// dropInterval is the period a queue dropping events is
	// logged over before it goes quiet.
	dropInterval = time.Minute

	// dropThreshold is the number of drops of one reason logged
	// in that period.
	dropThreshold = 3

	// dropOverflow is the reason recorded when a full queue gave
	// up on its oldest frames to make room for a new one.
	dropOverflow = "overflow"

	// dropTooLarge is the reason recorded when a frame was larger
	// than the whole queue and could never have been held.
	dropTooLarge = "too_large"
)

// A Producer is the input side of a sink, resolved at wiring so that
// dispatch is a single loop over every sink an event reaches. Whether
// a sink batches its events or consumes them directly is settled when
// the producer is built, never per event.
//
// Produce runs on every dispatch goroutine feeding the sink, so an
// implementation must hold no mutable state of its own.
type Producer interface {
	// Produce hands one event to the sink, and returns the buffer to
	// reuse for the next event. That is not the buffer it was given
	// whenever encoding had to grow it, so a caller must replace its
	// buffer with the result or the growth is lost.
	Produce([]byte, *event.Event) []byte
}

var _ Producer = (*directProducer)(nil)

// directProducer feeds a sink that consumes events as they are, with
// no wire format. Nothing is encoded, queued or retried, so an event
// is either recorded or refused by the time Produce returns.
type directProducer struct {
	sink    sink.EventSink
	metrics Metrics
}

// NewDirectProducer returns the input side of a sink consuming events directly.
func NewDirectProducer(s sink.EventSink, m Metrics) Producer {
	return &directProducer{sink: s, metrics: m}
}

// Produce implements the [Producer] interface.
func (p *directProducer) Produce(scratch []byte, ev *event.Event) []byte {
	// An error means the sink recorded nothing, which here is
	// a refusal rather than a failed delivery. There is no queue
	// to hold the event and no attempt to make again, so it is
	// only ever counted, and the sink names what it refused in
	// its own log.
	if err := p.sink.Handle(ev); err != nil {
		p.metrics.Rejected.Inc()
	} else {
		p.metrics.Delivered.Inc()
	}
	return scratch
}

var _ Producer = (*batchProducer)(nil)

// batchProducer encodes an event into a frame and queues it,
// leaving delivery to the drainer.
type batchProducer struct {
	name    string
	encoder sink.Encoder
	queue   *Queue
	drainer *Drainer
	metrics Metrics
	logger  *slog.Logger
	drops   *logsample.Gate[string]
}

func newBatchProducer(d *Drainer, m Metrics, logger *slog.Logger) *batchProducer {
	return &batchProducer{
		name:    d.sink.Name(),
		encoder: d.encoder,
		queue:   d.queue,
		drainer: d,
		metrics: m,
		logger:  logger,
		drops: logsample.PerInterval[string](
			dropInterval, dropThreshold,
		),
	}
}

// Produce implements the [Producer] interface.
func (p *batchProducer) Produce(buf []byte, ev *event.Event) []byte {
	buf = buffer.Shrink(buf, bufSizeThreshold)
	frame := p.encoder.AppendEvent(buf[:0], ev)

	// A frame the queue cannot hold is refused outright, where
	// a full queue accepts it and gives up as many of its oldest
	// frames as it needs.
	evicted, ok := p.queue.Push(frame)
	if !ok {
		p.metrics.TooLarge.Inc()
		p.dropped(dropTooLarge, len(frame), 1)

		return frame
	}
	if evicted.Count > 0 {
		p.metrics.Overflow.Add(float64(evicted.Count))
		p.dropped(dropOverflow, evicted.Bytes, evicted.Count)
	}
	// The drainer reads the queue under its own lock, so
	// waking occurs after the push and never during it.
	p.drainer.wake()

	return frame
}

// dropped logs events a queue could not keep, at a bounded rate.
func (p *batchProducer) dropped(reason string, size, count int) {
	suppressed, ok := p.drops.Admit(reason)
	if !ok {
		return
	}
	p.logger.LogAttrs(context.Background(), slog.LevelWarn, "queued events dropped",
		slog.String("reason", reason),
		slog.Int("bytes", size),
		slog.Int("count", count),
		suppressed,
	)
}
