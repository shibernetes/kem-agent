package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/shibernetes/kem-agent/internal/buffer"
	"github.com/shibernetes/kem-agent/internal/logsample"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	// payloadThreshold is the capacity in bytes above which a drainer
	// releases an accumulation or composed buffer it has stopped filling.
	// Both are batch-sized, where the buffer a producer encodes into is
	// event-sized and has a threshold of its own.
	payloadThreshold = 4 << 20 // 4 MiB
)

const (
	// sendFailInterval is the period a failing sink is logged over
	// before it goes quiet.
	sendFailInterval = time.Minute

	// sendFailThreshold is the number of failures of one send outcome
	// logged in that period. The first few often differ in useful detail.
	sendFailThreshold = 3
)

// A Drainer pops the frames a sink's producer has queued, gathers them
// into batches, and delivers each one to that sink.
//
// A sink has a single drainer, so its Send method is never called
// concurrently, however many pipelines feed it. That is what lets its
// framer carry state from one batch to the next.
type Drainer struct {
	sink      sink.BatchSink
	encoder   sink.Encoder
	framer    sink.Framer
	queue     *Queue
	producer  Producer
	config    sink.DrainerConfig
	metrics   Metrics
	logger    *slog.Logger
	sends     *logsample.Gate[sink.Result]
	oversized *logsample.Gate[int]
	payload   []byte
	composed  []byte
	done      chan struct{}
	mu        sync.Mutex
	cond      *sync.Cond
	linger    *time.Timer
	deadline  time.Time
	stopping  bool
	parent    context.Context
}

// NewDrainer returns a drainer delivering to a batch sink.
func NewDrainer(s sink.BatchSink, cfg sink.DrainerConfig, m Metrics, logger *slog.Logger) *Drainer {
	d := &Drainer{
		sink:      s,
		encoder:   s.Encoder(),
		framer:    s.Framer(),
		queue:     NewQueue(int(cfg.Queue.MaxBytes)),
		config:    cfg,
		metrics:   m,
		logger:    logger,
		oversized: logsample.OnChange[int](),
		done:      make(chan struct{}),
		parent:    context.Background(),
		sends: logsample.PerInterval[sink.Result](
			sendFailInterval,
			sendFailThreshold,
		),
	}
	d.cond = sync.NewCond(&d.mu)
	d.producer = newBatchProducer(d, m, logger)

	// A single timer, created stopped, bounds every batch,
	// reset before a new one opens.
	d.linger = time.AfterFunc(time.Duration(cfg.Batch.Timeout), d.wake)
	d.linger.Stop()

	return d
}

// Run delivers batches until Stop is called, and returns once the
// queue content has been flushed. A drainer runs once and cannot
// be restarted.
func (d *Drainer) Run() {
	defer close(d.done)

	// fill returns no frames only once the stop path has drained
	// the queue, so it is the signal that breaks the loop rather
	// than the stop flag itself.
	for {
		frames := d.fill()
		if frames == 0 {
			return
		}
		d.deliver(d.sendContext(), frames)
	}
}

// Stop ends the drain loop and waits for the queue content to be
// flushed, bounded by ctx. It returns nil once the queue is empty,
// or the context's error if the drain outlasts it, leaving what
// is still queued undelivered.
//
// Delivery keeps running past a stop, so ctx bounds the drain and
// nothing else. Cancelling the context the drainer ran under would
// instead fail every remaining attempt at once and discard the
// queue's remaining content.
func (d *Drainer) Stop(ctx context.Context) error {
	d.mu.Lock()
	d.stopping = true
	d.parent = ctx
	d.cond.Broadcast()
	d.mu.Unlock()

	select {
	case <-d.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Producer returns the input side of the sink this drainer delivers to.
func (d *Drainer) Producer() Producer {
	return d.producer
}

// Usage returns the frame bytes the sink's queue is holding.
func (d *Drainer) Usage() int {
	return d.queue.Usage()
}

// fill gathers frames into the accumulation buffer until one of the
// batch limits is reached, and returns how many frames it gathered.
func (d *Drainer) fill() int {
	d.payload = buffer.Shrink(d.payload, payloadThreshold)[:0]

	var (
		sep       = d.framer.Separator()
		fixed     = d.framer.Fixed()
		maxBytes  = int(d.config.Batch.MaxBytes)
		maxEvents = d.config.Batch.MaxEvents
		frames    int
	)
	for {
		// The first frame of a batch is popped whatever its size is.
		// The byte limit bounds what the drainer builds rather than
		// what the sink accepts, so holding a frame to it would leave
		// one larger than a whole batch refused by every batch in turn.
		limit := 0
		if frames > 0 && maxBytes > 0 {
			if limit = maxBytes - fixed - len(d.payload) - len(sep); limit <= 0 {
				break
			}
		}
		idx := len(d.payload)
		if frames > 0 {
			d.payload = append(d.payload, sep...)
		}
		var n int
		if d.payload, n = d.queue.Pop(d.payload, limit); n == 0 {
			if idx < len(d.payload) {
				d.payload = d.payload[:idx]
			}
			// Pop returns nothing for an empty queue and for a frame
			// larger than the available space in a batch. The first
			// waits for more, the second closes the batch, and the queue
			// length is the only thing that can differentiate the two.
			//
			// The first frame carries no limit, so nothing but an empty
			// queue can refuse it and the length would only report what
			// a producer pushed in the meantime.
			if (frames > 0 && d.queue.Len() > 0) || !d.await() {
				break
			}
			continue
		}
		frames++
		if frames == 1 {
			d.arm()
		}
		if maxEvents > 0 && frames >= maxEvents {
			break
		}
	}
	d.disarm()

	return frames
}

// deliver composes the frames gathered into one payload and hands
// it to the sink, retrying a failed delivery until it succeeds or
// one of the retry limits is reached.
//
// The identifier is generated once, before the first attempt, so that
// a destination deduplicating on it recognizes a batch it has already
// seen rather than considering every retry as new.
func (d *Drainer) deliver(ctx context.Context, frames int) {
	d.composed = buffer.Shrink(d.composed, payloadThreshold)
	d.composed = d.framer.Compose(d.composed[:0], d.payload, frames)
	d.send(ctx, d.composed, sink.NewBatchID(), frames)
}

// send delivers one payload, and accounts for every event in it
// exactly once, whether it was accepted, refused or discarded.
func (d *Drainer) send(ctx context.Context, payload []byte, id sink.BatchID, frames int) {
	var (
		timeout  = time.Duration(d.config.SendTimeout)
		deadline = time.Now().Add(time.Duration(d.config.Retry.Timeout))
		interval = time.Duration(d.config.Retry.InitialInterval)
	)
	for {
		// The deadline bounds one attempt, and the cause is what makes
		// the error recognizable. A sink with no timeout gets none, it
		// would hand every attempt an expired deadline.
		var (
			attempt = ctx
			cancel  = func() {}
		)
		if timeout > 0 {
			attempt, cancel = context.WithTimeoutCause(ctx, timeout, sink.ErrSendTimeout)
		}
		start := time.Now()
		refused, err := d.sink.Send(attempt, payload, id)
		cancel()

		result := sink.Classify(err)
		d.metrics.SendDuration.WithLabelValues(result.String()).Observe(time.Since(start).Seconds())

		switch result {
		case sink.ResultSuccess:
			// A destination may accept the request but still refuse some
			// of the records within it, which is a loss the delivery count
			// must not absorb. The count comes off the wire and a counter
			// panics on a negative, so it is held to the batch it describes.
			refused = clamp(refused, 0, frames)
			d.metrics.Delivered.Add(float64(frames - refused))
			if refused > 0 {
				d.metrics.Rejected.Add(float64(refused))
			}
			return
		case sink.ResultOversized:
			d.dropOversized(ctx, len(payload), frames)
			return
		}
		if suppressed, ok := d.sends.Admit(result); ok {
			d.logger.LogAttrs(ctx, slog.LevelWarn, "batch delivery failed",
				slog.String("result", result.String()),
				slog.Int("frames", frames),
				slog.Any("error", err),
				suppressed,
			)
		}
		if result == sink.ResultPermanent {
			d.metrics.PermanentError.Add(float64(frames))
			return
		}
		wait := jitter(interval)
		if retryAfter, ok := errors.AsType[*sink.RetryAfterError](err); ok {
			// A delay the destination returned raises the backoff floor.
			// The batch's retry timeout still bounds it, so a delay longer
			// than the remaining limit drops the batch rather than holding
			// the drainer while its queue evicts what follows.
			wait = jitter(max(interval, retryAfter.Delay))
		}
		if !d.config.Retry.Enabled || time.Now().Add(wait).After(deadline) {
			d.metrics.RetryExhausted.Add(float64(frames))
			return
		}
		select {
		case <-ctx.Done():
			// The shutdown timeout reached its own limit with the batch undelivered.
			d.metrics.RetryExhausted.Add(float64(frames))
			return
		case <-time.After(wait):
		}
		interval = min(
			time.Duration(float64(interval)*d.config.Retry.Multiplier),
			time.Duration(d.config.Retry.MaxInterval),
		)
	}
}

// await blocks until there is another frame for the batch being filled,
// and reports whether there is one. It gives up once the linger timeout
// has run out on the frames already gathered, or once the stop leaves
// the queue empty.
//
// A batch holding no frames carries no deadline, so the wait is
// unbounded and ends only on the next push or on stop.
func (d *Drainer) await() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	for !d.stopping && d.queue.Len() == 0 {
		if !d.deadline.IsZero() && !time.Now().Before(d.deadline) {
			return false
		}
		d.cond.Wait()
	}
	// A stop drains what is left rather than abandoning it, so
	// the queue decides here and the stop only ends the wait.
	return d.queue.Len() > 0
}

// dropOversized drops a payload the destination refused for its size.
// Sending the same bytes again cannot succeed, so the batch is discarded.
func (d *Drainer) dropOversized(ctx context.Context, size, frames int) {
	d.metrics.PermanentError.Add(float64(frames))

	if suppressed, ok := d.oversized.Admit(size); ok {
		d.logger.LogAttrs(ctx, slog.LevelWarn, "batch refused as oversized and dropped",
			slog.Int("bytes", size),
			slog.Int("frames", frames),
			suppressed,
		)
	}
}

// wake wakes the drainer, for a frame a producer has just pushed or
// when the linger timeout runs out on the open batch.
//
// Holding the lock is what makes the wakeup reliable rather than what
// the broadcast itself needs. The drainer finds the queue empty and
// blocks on it under this lock, so a broadcast raised without it can
// land between finding it empty and blocking, wake nobody, and leave
// the frame until the next push.
//
// It only ever broadcasts. What decides whether the open batch is
// closed is the deadline, so a firing left over from a batch already
// delivered costs a wakeup and nothing more.
func (d *Drainer) wake() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cond.Broadcast()
}

// arm starts the linger timeout for a new batch.
func (d *Drainer) arm() {
	d.mu.Lock()
	defer d.mu.Unlock()

	timeout := time.Duration(d.config.Batch.Timeout)
	d.deadline = time.Now().Add(timeout)
	d.linger.Reset(timeout)
}

// disarm ends the linger timeout for a batch that is done accumulating.
func (d *Drainer) disarm() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.deadline = time.Time{}
	d.linger.Stop()
}

// sendContext returns the context used for delivery attempts. It is the
// background context while the drainer runs, and the shutdown's one once
// a graceful shutdown has been initiated. A batch already in flight keeps
// the one it started under, so a stop takes effect from the next batch.
func (d *Drainer) sendContext() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.parent
}

// jitter spreads a backoff interval by up to half its length.
func jitter(d time.Duration) time.Duration {
	return d + rand.N(d/2+1) //nolint:gosec
}

// clamp holds v within the lo and hi bounds.
func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
