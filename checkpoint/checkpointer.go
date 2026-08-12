package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/units"
)

const (
	// FinalSaveReserve is the share of the shutdown timeout kept for the
	// final checkpoint write, which a slow drain would otherwise consume.
	FinalSaveReserve = units.Duration(5 * time.Second)
)

// A Reporter reports how far watches has been read.
type Reporter interface {
	// Positions returns a fresh snapshot of the current positions,
	// ignoring watches that hold no position yet.
	Positions() map[string]string
}

// A Checkpointer is the sole writer of a checkpoint [State].
// It records the furthest read position of every watch, so that
// a restart resumes from there rather than replaying every event
// the APIServer retains.
type Checkpointer struct {
	store    Store
	reporter Reporter
	interval time.Duration
	metrics  Metrics
	logger   *slog.Logger
	last     map[string]string
	done     chan struct{}
}

// Config defines the configuration for a [Checkpointer].
type Config struct {
	SaveInterval units.Duration `yaml:"save_interval,omitempty"`
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if c.SaveInterval <= 0 {
		return diag.Pathf("save_interval", "save_interval must be positive")
	}
	return nil
}

// NewCheckpointer returns a checkpointer recording the positions a
// [Reporter] reports into a store.
func NewCheckpointer(store Store, reporter Reporter, cfg Config, m Metrics, logger *slog.Logger) *Checkpointer {
	return &Checkpointer{
		store:    store,
		reporter: reporter,
		interval: time.Duration(cfg.SaveInterval),
		metrics:  m,
		logger:   logger,
		done:     make(chan struct{}),
	}
}

// Wait blocks until Run has returned, which the final write depends on.
func (c *Checkpointer) Wait() {
	<-c.done
}

// Run writes the positions every interval until the context is done.
// A checkpointer runs once and cannot be restarted.
//
// The final write is not done here. It belongs to the stop sequence,
// after the queues have drained and using a separate timeout.
func (c *Checkpointer) Run(ctx context.Context) {
	defer close(c.done)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Write(ctx)
		}
	}
}

// Read returns the state to resume from, or an empty state when
// there is none yet.
func (c *Checkpointer) Read(ctx context.Context) (State, error) {
	state, err := c.store.Load(ctx)
	switch {
	case errors.Is(err, ErrNotFound):
		c.logger.LogAttrs(ctx, slog.LevelInfo, "no checkpoint to resume from")
		return State{}, nil
	case errors.Is(err, ErrCorrupt):
		c.logger.LogAttrs(ctx, slog.LevelWarn, "discarded an unreadable checkpoint", slog.Any("error", err))
		return State{}, nil
	case err != nil:
		return State{}, fmt.Errorf("failed to read the checkpoint: %w", err)
	}
	// Seeding the baseline avoids rewriting a state that has not moved.
	c.last = maps.Clone(state.Watches)

	return state, nil
}

// Write records the positions, unless they are unchanged since the
// last write. A failure is logged and counted rather than returned,
// because the agent still ingests and delivers and the next write
// repairs it.
func (c *Checkpointer) Write(ctx context.Context) {
	positions := c.reporter.Positions()
	if maps.Equal(positions, c.last) {
		c.metrics.Skipped.Inc()
		return
	}
	state := State{Watches: positions}
	if err := c.store.Save(ctx, state); err != nil {
		c.metrics.Failure.Inc()
		c.logger.LogAttrs(ctx, slog.LevelError, "failed to save checkpoint", slog.Any("error", err))
		return
	}
	c.metrics.Success.Inc()

	// The updated positions become the baseline the next write
	// compares against.
	c.last = maps.Clone(positions)
}
