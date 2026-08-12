package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/sink"
)

func TestDrainerCountsPartialSuccess(t *testing.T) {
	cfg := testConfig()
	cfg.Batch.MaxEvents = 5

	var (
		s    = newFakeBatchSink()
		d, r = newTestDrainer(t, s, cfg)
	)
	// The destination accepts the request and refuses two of the
	// records it contains, which the delivery count must not absorb.
	s.responses = []response{{refused: 2}}

	push(t, d, 5, 10)
	drain(t, d)

	if got := r.delivered.get(); got != 3 {
		t.Errorf("got %v events delivered, want 3", got)
	}
	if got := r.rejected.get(); got != 2 {
		t.Errorf("got %v events rejected, want 2", got)
	}
}

func TestDrainerClampsRefusedCount(t *testing.T) {
	var (
		s    = newFakeBatchSink()
		d, r = newTestDrainer(t, s, testConfig())
	)
	// The rejected events count comes from the sink response, and
	// one larger than the batch would take the delivery counter
	// below zero, which panics.
	s.responses = []response{{refused: 99}}

	push(t, d, 2, 10)
	drain(t, d)

	if got := r.delivered.get(); got != 0 {
		t.Errorf("got %v events delivered, want none", got)
	}
	if got := r.rejected.get(); got != 2 {
		t.Errorf("got %v events rejected, want 2", got)
	}
}

func TestDrainerDropsPermanentFailure(t *testing.T) {
	var (
		s    = newFakeBatchSink()
		d, r = newTestDrainer(t, s, retrying(testConfig()))
	)
	s.responses = []response{{err: fmt.Errorf("refused: %w", sink.ErrPermanent)}}

	push(t, d, 3, 10)
	drain(t, d)

	if got := r.permanent.get(); got != 3 {
		t.Errorf("got %v events dropped, want 3", got)
	}
	if got := len(s.payloads()); got != 1 {
		t.Errorf("got %d attempts, want a permanent failure not retried", got)
	}
}

func TestDrainerDropsOversizedBatch(t *testing.T) {
	var (
		s    = newFakeBatchSink()
		d, r = newTestDrainer(t, s, retrying(testConfig()))
	)
	s.responses = []response{{err: sink.ErrOversized}}

	push(t, d, 3, 10)
	drain(t, d)

	if got := r.permanent.get(); got != 3 {
		t.Errorf("got %v events dropped, want 3", got)
	}
	if got := r.sends.get(sink.ResultOversized); got != 1 {
		t.Errorf("got %d oversized attempts recorded, want 1", got)
	}
}

func TestDrainerRetriesUntilAccepted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			s    = newFakeBatchSink()
			d, r = newTestDrainer(t, s, retrying(testConfig()))
		)
		s.responses = []response{
			{err: errors.New("unavailable")},
			{err: errors.New("unavailable")},
			{},
		}
		push(t, d, 2, 10)
		drain(t, d)

		if got := len(s.payloads()); got != 3 {
			t.Fatalf("got %d attempts, want 3 (two failures and the success)", got)
		}
		// One identifier throughout, so a destination deduplicating
		// on it recognizes the retries as the batch it already saw.
		ids := s.batchIDs()
		if ids[0] != ids[1] || ids[1] != ids[2] {
			t.Errorf("got %v, want one identifier across every attempt", ids)
		}
		if got := r.delivered.get(); got != 2 {
			t.Errorf("got %v events delivered, want 2", got)
		}
		if got := r.sends.get(sink.ResultRetryable); got != 2 {
			t.Errorf("got %d retryable attempts recorded, want 2", got)
		}
	})
}

func TestDrainerExhaustsRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := retrying(testConfig())
		cfg.Retry.Timeout = units.Duration(50 * time.Millisecond)

		var (
			s    = newFakeBatchSink()
			d, r = newTestDrainer(t, s, cfg)
		)
		s.responses = []response{{err: errors.New("unavailable")}}

		push(t, d, 4, 10)
		drain(t, d)

		if got := r.exhausted.get(); got != 4 {
			t.Errorf("got %v events dropped, want 4", got)
		}
		if got := r.delivered.get(); got != 0 {
			t.Errorf("got %v events delivered, want none", got)
		}
	})
}

func TestDrainerWaitsTheRequestedDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const delay = 5 * time.Second

		cfg := retrying(testConfig())
		cfg.Retry.Timeout = units.Duration(time.Minute)

		var (
			s    = newFakeBatchSink()
			d, _ = newTestDrainer(t, s, cfg)
		)
		s.responses = []response{
			{err: sink.NewRetryAfterError(errors.New("busy"), delay)},
			{},
		}
		start := time.Now()
		push(t, d, 1, 10)
		drainWithin(t, d, delay+5*time.Second)

		// The backoff ladder starts at ten milliseconds, so only the
		// delay the destination asked for accounts for the wait.
		if elapsed := time.Since(start); elapsed < delay {
			t.Errorf("got a wait of %v, want at least %v", elapsed, delay)
		}
	})
}

func TestDrainerDropsWhenTheDelayOutlastsTheLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := retrying(testConfig())
		cfg.Retry.Timeout = units.Duration(time.Second)

		var (
			s    = newFakeBatchSink()
			d, r = newTestDrainer(t, s, cfg)
		)
		// An hour is longer than the batch has left, so the delay is
		// refused rather than waited out while the queue backs up.
		s.responses = []response{
			{err: sink.NewRetryAfterError(errors.New("busy"), time.Hour)},
		}
		push(t, d, 1, 10)
		drain(t, d)

		if got := len(s.payloads()); got != 1 {
			t.Errorf("got %d attempts, want the delay refused", got)
		}
		if got := r.exhausted.get(); got != 1 {
			t.Errorf("got %v events dropped, want 1", got)
		}
	})
}

func TestDrainerDropsWhenTheShutdownExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := retrying(testConfig())
		cfg.Retry.InitialInterval = units.Duration(time.Minute)
		cfg.Retry.Timeout = units.Duration(time.Hour)

		var (
			s    = newFakeBatchSink()
			d, r = newTestDrainer(t, s, cfg)
		)
		s.responses = []response{{err: errors.New("unavailable")}}

		// A shutdown running out while the drainer waits to try again
		// drops the batch, rather than waiting for a retry it will not
		// have the time to do.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		d.send(ctx, []byte("frame"), sink.NewBatchID(), 2)

		if got := len(s.payloads()); got != 1 {
			t.Errorf("got %d attempts, want the backoff cut short", got)
		}
		if got := r.exhausted.get(); got != 2 {
			t.Errorf("got %v events dropped, want 2", got)
		}
	})
}

func TestDrainerRecordsSendTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		cfg.SendTimeout = units.Duration(100 * time.Millisecond)

		var (
			s    = newFakeBatchSink()
			d, r = newTestDrainer(t, s, cfg)
		)
		// A destination that is unreachable triggers the attempt's
		// deadline, which carries ErrSendTimeout as its cause and
		// is classified as a timeout rather than a retryable error.
		s.block = make(chan struct{})
		defer close(s.block)

		push(t, d, 1, 10)
		drain(t, d)

		if got := r.sends.get(sink.ResultTimeout); got != 1 {
			t.Errorf("got %d timed out attempts, want 1", got)
		}
		if got := r.exhausted.get(); got != 1 {
			t.Errorf("got %v events dropped, want 1", got)
		}
	})
}

func TestDrainerSendsWithoutTimeout(t *testing.T) {
	s := newFakeBatchSink()
	cfg := testConfig()
	cfg.SendTimeout = 0

	d, _ := newTestDrainer(t, s, cfg)

	push(t, d, 1, 8)
	drain(t, d)

	got := s.contextErrors()
	if len(got) != 1 {
		t.Fatalf("got %d sends, want 1", len(got))
	}
	if got[0] != nil {
		t.Errorf("the send context was already done: %v", got[0])
	}
}

// retrying returns a config that reattempts a failed delivery with
// a quick backoff, where the default settings do not retry at all.
func retrying(cfg sink.DrainerConfig) sink.DrainerConfig {
	cfg.Retry = sink.RetryConfig{
		Enabled:         true,
		Timeout:         units.Duration(time.Minute),
		InitialInterval: units.Duration(10 * time.Millisecond),
		MaxInterval:     units.Duration(100 * time.Millisecond),
		Multiplier:      2,
	}
	return cfg
}
