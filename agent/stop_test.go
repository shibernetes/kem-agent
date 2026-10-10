package agent

import (
	"context"
	"log/slog"
	"slices"
	"testing"
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
