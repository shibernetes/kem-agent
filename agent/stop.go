package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/shibernetes/kem-agent/checkpoint"
)

// stop releases the agent's components.
//
// Every phase gets a fresh context with its own deadline. The run's
// context is already canceled, so using it would fail every send at
// once and drop what the queues still hold.
func (a *Agent) stop() {
	ctx := context.Background()
	a.log.LogAttrs(ctx, slog.LevelInfo, "stopping agent")

	var (
		deadline = time.Now().Add(time.Duration(a.cfg.Service.ShutdownTimeout))
		reserve  = time.Duration(checkpoint.FinalSaveReserve)
	)
	// A start that stopped before the watches opened has no watches
	// to join, no checkpointer running to wait for, and no position
	// to record. A checkpoint write replaces the whole state, so
	// saving here would wipe what is stored.
	running := a.stopWatches != nil

	if running {
		a.stopAndWaitWatches()
	}
	a.drainSinks(deadline.Add(-reserve))

	if running {
		a.saveCheckpoint(reserve)
	}
	a.releaseComponents(deadline)
	a.shutdownServers(deadline)

	a.log.LogAttrs(ctx, slog.LevelInfo, "agent stopped")
}

// stopAndWaitWatches ends the watches and waits for them to return,
// so nothing records a position while the final write reads them.
func (a *Agent) stopAndWaitWatches() {
	a.stopWatches()
	<-a.sourceDone

	// The checkpointer is the sole writer of the state, so
	// it has to return before the final write runs.
	a.checkpointer.Wait()
}

// drainSinks flushes what every sink still holds, all of them at once
// so that one slow destination does not waste the others' time.
func (a *Agent) drainSinks(deadline time.Time) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	var wg sync.WaitGroup
	for _, entry := range a.sinks {
		// Stop waits for the drain loop to end, which never runs for
		// a drainer that was not started.
		if entry.drainer == nil || !entry.started {
			continue
		}
		wg.Go(func() {
			if err := entry.drainer.Stop(ctx); err != nil {
				a.log.LogAttrs(ctx, slog.LevelWarn, "sink did not drain in time",
					slog.String("sink", entry.name),
					slog.Any("error", err),
				)
			}
		})
	}
	wg.Wait()
}

// saveCheckpoint records the positions the watches reached,
// on the share of the shutdown timeout reserved for it.
func (a *Agent) saveCheckpoint(reserve time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), reserve)
	defer cancel()

	a.checkpointer.Write(ctx)
}

// releaseComponents releases the sinks and the fan-outs, using
// whatever is left of the shutdown timeout.
func (a *Agent) releaseComponents(deadline time.Time) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	if err := a.Close(ctx); err != nil {
		a.log.LogAttrs(ctx, slog.LevelWarn, "failed to release a component",
			slog.Any("error", err),
		)
	}
}

// shutdownServers stops the listeners last, so what the drain
// and the final write record stays scrapable to the end.
func (a *Agent) shutdownServers(deadline time.Time) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	if a.pprof != nil {
		a.pprof.shutdown(ctx)
	}
	if a.server != nil {
		a.server.shutdown(ctx)
	}
}
