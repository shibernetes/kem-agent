package pipeline

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestDrainerStopDrainsQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		cfg.Batch.MaxEvents = 10

		var (
			s    = newFakeBatchSink()
			d, r = newTestDrainer(t, s, cfg)
		)
		go d.Run()

		// The stop arrives with frames still queued, and
		// none of them should be abandoned because of it.
		push(t, d, 25, 10)
		stop(t, d)

		if got := r.delivered.get(); got != 25 {
			t.Errorf("got %v events delivered, want the 25 queued", got)
		}
	})
}

func TestDrainerWakesOnProduce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()

		var (
			s    = newFakeBatchSink()
			d, _ = newTestDrainer(t, s, cfg)
		)
		go d.Run()

		// When nothing is queued the drainer blocks with no
		// deadline, so waiting for it to settle asserts that
		// a push wakes it up.
		synctest.Wait()
		push(t, d, 1, 10)

		time.Sleep(time.Duration(cfg.Batch.Timeout))
		synctest.Wait()

		if got := len(s.payloads()); got != 1 {
			t.Errorf("got %d batches delivered, want the push to wake the drainer", got)
		}
		stop(t, d)
	})
}

func TestDrainerStopReportsTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newFakeBatchSink()
		s.block = make(chan struct{})
		d, _ := newTestDrainer(t, s, testConfig())

		go d.Run()
		push(t, d, 1, 10)

		// When the sink does not reply, the drain cannot
		// finish and the shutdown gives up on its own deadline.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		if err := d.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v, want the context deadline exceeded", err)
		}
		close(s.block)
		synctest.Wait()
	})
}

func TestDrainerStopsBeforeRunning(t *testing.T) {
	// A shutdown can reach a drainer that has not started yet,
	// which is what a startup failing part way through produces.
	synctest.Test(t, func(t *testing.T) {
		var (
			s    = newFakeBatchSink()
			d, _ = newTestDrainer(t, s, testConfig())
		)
		stopped := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stopped <- d.Stop(ctx)
		}()
		// Goroutines begin executing independently. Waiting for
		// the shutdown to block establishes the relative ordering
		// this test needs.
		synctest.Wait()
		go d.Run()

		if err := <-stopped; err != nil {
			t.Errorf("got %v, want a drainer that never ran to stop cleanly", err)
		}
		if got := len(s.payloads()); got != 0 {
			t.Errorf("got %d batches delivered, want none", got)
		}
	})
}
