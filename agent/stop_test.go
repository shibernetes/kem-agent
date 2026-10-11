package agent

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/config"
	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/pipeline"
	"github.com/shibernetes/kem-agent/sink"
)

// TestStopDrainsSinksBeforeFinalSave asserts that the stop sequence delivers
// what the sinks still hold before it saves the final checkpoint. A checkpoint
// saved first would record positions past events that were never delivered.
func TestStopDrainsSinksBeforeFinalSave(t *testing.T) {
	var (
		calls   = &callLog{}
		logger  = slog.New(slog.DiscardHandler)
		metrics = newInstruments(prometheus.NewRegistry())
		out     = &fakeBatchSink{calls: calls}
	)
	// The batch stays open for the length of the test, so only the
	// drain can send it.
	drainer := pipeline.NewDrainer(out, sink.DrainerConfig{
		Batch:       sink.BatchConfig{MaxEvents: 2, Timeout: units.Duration(time.Hour)},
		Queue:       sink.DefaultQueueConfig(),
		Retry:       sink.DefaultRetryConfig(),
		SendTimeout: units.Duration(time.Second),
	}, metrics.forBatchSink("out"), logger)

	go drainer.Run()
	drainer.Producer().Produce(nil, &event.Event{
		Name: "pending",
	})
	checkpointer := checkpoint.NewCheckpointer(
		&fakeStore{calls: calls},
		reporterFunc(func() map[string]string { return map[string]string{"team-a": "1"} }),
		checkpoint.Config{SaveInterval: units.Duration(time.Hour)},
		metrics.forCheckpoint(),
		logger,
	)
	// The stop sequence waits for the checkpointer and the watches,
	// so both have already returned when it runs.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checkpointer.Run(ctx)

	sourceDone := make(chan struct{})
	close(sourceDone)

	a := &Agent{
		cfg:          &config.Config{Service: config.DefaultServiceConfig()},
		sinks:        []*sinkEntry{{name: "out", sink: out, drainer: drainer, started: true}},
		checkpointer: checkpointer,
		log:          logger,
		stopWatches:  func() {},
		sourceDone:   sourceDone,
	}
	a.stop()

	if got, want := calls.list(), []string{"send", "save"}; !slices.Equal(got, want) {
		t.Errorf("got calls %v, want %v", got, want)
	}
}

// TestStopWithStalledCheckpointSave asserts that the stop sequence still
// drains the sinks and returns within the shutdown timeout when a checkpoint
// save is blocked.
func TestStopWithStalledCheckpointSave(t *testing.T) {
	cases := map[string]time.Duration{
		"periodic save": 15 * time.Second,
		"final save":    time.Hour,
	}
	for name, interval := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var (
					calls   = &callLog{}
					logger  = slog.New(slog.DiscardHandler)
					metrics = newInstruments(prometheus.NewRegistry())
					out     = &fakeBatchSink{calls: calls}
					store   = &fakeStore{calls: calls, block: make(chan struct{})}
				)
				defer close(store.block)

				drainer := pipeline.NewDrainer(out, sink.DrainerConfig{
					Batch:       sink.BatchConfig{MaxEvents: 2, Timeout: units.Duration(time.Hour)},
					Queue:       sink.DefaultQueueConfig(),
					Retry:       sink.DefaultRetryConfig(),
					SendTimeout: units.Duration(time.Second),
				}, metrics.forBatchSink("out"), logger)

				go drainer.Run()
				drainer.Producer().Produce(nil, &event.Event{
					Name: "pending",
				})
				checkpointer := checkpoint.NewCheckpointer(
					store,
					reporterFunc(func() map[string]string { return map[string]string{"team-a": "1"} }),
					checkpoint.Config{SaveInterval: units.Duration(interval)},
					metrics.forCheckpoint(),
					logger,
				)
				ctx, cancel := context.WithCancel(t.Context())
				go checkpointer.Run(ctx)

				// Long enough for the shorter interval to start a save.
				time.Sleep(20 * time.Second)
				synctest.Wait()
				cancel()

				sourceDone := make(chan struct{})
				close(sourceDone)

				agent := &Agent{
					cfg:          &config.Config{Service: config.DefaultServiceConfig()},
					sinks:        []*sinkEntry{{name: "out", sink: out, drainer: drainer, started: true}},
					checkpointer: checkpointer,
					log:          logger,
					stopWatches:  func() {},
					sourceDone:   sourceDone,
				}
				start := time.Now()
				agent.stop()

				shutdownTimeout := time.Duration(agent.cfg.Service.ShutdownTimeout)

				if got := time.Since(start); got > shutdownTimeout {
					t.Errorf("the stop sequence took %s, want at most %s", got, shutdownTimeout)
				}
				if !slices.Contains(calls.list(), "send") {
					t.Error("the pending batch was not sent, want the sinks drained")
				}
			})
		})
	}
}
