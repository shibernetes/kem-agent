package checkpoint

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shibernetes/kem-agent/config/units"
)

func TestReadReturnsStoredState(t *testing.T) {
	h := newHarness(t, time.Minute)

	h.store.state = State{Watches: map[string]string{"team-a": "42"}}
	state, err := h.cp.Read(context.Background())
	if err != nil {
		t.Fatalf("failed to read state: %v", err)
	}
	if want := map[string]string{"team-a": "42"}; !maps.Equal(state.Watches, want) {
		t.Errorf("got watches %v, want %v", state.Watches, want)
	}
}

func TestReadSeedsLastState(t *testing.T) {
	h := newHarness(t, time.Minute)
	h.store.state = State{Watches: map[string]string{"team-a": "42"}}
	h.reporter.set(map[string]string{"team-a": "42"})

	// Read is expected to keep what it read as the last state,
	// so the next Write must be skipped, and the store left
	// untouched.
	if _, err := h.cp.Read(context.Background()); err != nil {
		t.Fatalf("failed to read the state: %v", err)
	}
	h.cp.Write(context.Background())

	if n := len(h.store.attempts()); n != 0 {
		t.Errorf("got %d writes, want the stored state left alone", n)
	}
	if got := h.metrics.skipped.get(); got != 1 {
		t.Errorf("got %v skipped writes, want 1", got)
	}
}

// A store holding nothing and a store holding an unreadable state
// both resume from scratch. The level they report at is what tells
// the first run apart from a corrupt state.
func TestReadResumesFromNothing(t *testing.T) {
	cases := map[string]struct {
		err   error
		level slog.Level
	}{
		"no state":         {err: ErrNotFound, level: slog.LevelInfo},
		"unreadable state": {err: ErrCorrupt, level: slog.LevelWarn},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, time.Minute)
			h.store.loadErr = tc.err

			state, err := h.cp.Read(context.Background())
			if err != nil {
				t.Fatalf("failed to read state: %v", err)
			}
			if len(state.Watches) != 0 {
				t.Errorf("got watches %v, want none", state.Watches)
			}
			levels := h.logs.levels()
			if len(levels) != 1 {
				t.Fatalf("got %d log records, want 1", len(levels))
			}
			if levels[0] != tc.level {
				t.Errorf("got log level %s, want %s", levels[0], tc.level)
			}
		})
	}
}

func TestReadFailsOnUnreachableStore(t *testing.T) {
	h := newHarness(t, time.Minute)
	h.store.loadErr = errors.New("connection refused")

	// The positions may be intact, so resuming from
	// nothing would replay for no reason.
	if _, err := h.cp.Read(context.Background()); err == nil {
		t.Error("got no error, want an unreachable store to fail")
	}
	// The agent reports a fatal error once, where it exits.
	if n := len(h.logs.levels()); n != 0 {
		t.Errorf("got %d log records, want the failure left to the caller", n)
	}
}

func TestWriteRecordsPositions(t *testing.T) {
	h := newHarness(t, time.Minute)
	h.reporter.set(map[string]string{"team-a": "42"})

	h.cp.Write(context.Background())

	state := lastAttempt(t, h.store, 1)
	if want := map[string]string{"team-a": "42"}; !maps.Equal(state.Watches, want) {
		t.Errorf("got watches %v, want %v", state.Watches, want)
	}
	if got := h.metrics.success.get(); got != 1 {
		t.Errorf("got %v successful writes, want 1", got)
	}
}

func TestWriteSkipsUnchangedState(t *testing.T) {
	h := newHarness(t, time.Minute)
	h.reporter.set(map[string]string{"team-a": "42"})

	h.cp.Write(context.Background())
	h.cp.Write(context.Background())

	if n := len(h.store.attempts()); n != 1 {
		t.Errorf("got %d writes, want the second one skipped", n)
	}
	if got := h.metrics.skipped.get(); got != 1 {
		t.Errorf("got %v skipped writes, want 1", got)
	}
}

func TestWriteReplacesWholeState(t *testing.T) {
	h := newHarness(t, time.Minute)
	h.reporter.set(map[string]string{"team-a": "42", "team-b": "17"})
	h.cp.Write(context.Background())

	h.reporter.set(map[string]string{"team-a": "43"})
	h.cp.Write(context.Background())

	// A watch removed from the configuration must be cleared
	// from the positions, or it would resume from a stale resource
	// version if it is added back later.
	state := lastAttempt(t, h.store, 2)
	if want := map[string]string{"team-a": "43"}; !maps.Equal(state.Watches, want) {
		t.Errorf("got watches %v, want %v", state.Watches, want)
	}
}

func TestWriteRetriesAfterFailure(t *testing.T) {
	h := newHarness(t, time.Minute)
	h.reporter.set(map[string]string{"team-a": "42"})
	h.store.saveErr = errors.New("connection refused")

	h.cp.Write(context.Background())

	if got := h.metrics.failure.get(); got != 1 {
		t.Errorf("got %v failed writes, want 1", got)
	}
	levels := h.logs.levels()
	if len(levels) != 1 {
		t.Fatalf("got %d log records, want 1", len(levels))
	}
	if levels[0] != slog.LevelError {
		t.Errorf("got log level %s, want %s", levels[0], slog.LevelError)
	}
	// The last state is updated only on a successful write, so
	// the same positions are written again rather than skipped.
	h.store.saveErr = nil
	h.cp.Write(context.Background())

	if got := h.metrics.success.get(); got != 1 {
		t.Errorf("got %v successful writes, want the state written again", got)
	}
	if n := len(h.store.attempts()); n != 2 {
		t.Errorf("got %d write attempts, want 2", n)
	}
}

func TestWriteCopyLastState(t *testing.T) {
	h := newHarness(t, time.Minute)
	h.reporter.noCopy = true
	h.reporter.set(map[string]string{"team-a": "42"})

	h.cp.Write(context.Background())
	h.reporter.advance("team-a", "43")
	h.cp.Write(context.Background())

	// The last successfully saved state is copied, so positions
	// still count as changed when a reporter writes into the map
	// it returned.
	state := lastAttempt(t, h.store, 2)
	if want := map[string]string{"team-a": "43"}; !maps.Equal(state.Watches, want) {
		t.Errorf("got watches %v, want %v", state.Watches, want)
	}
}

func TestRunWritesEveryInterval(t *testing.T) {
	const interval = time.Second

	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, interval)
		h.reporter.set(map[string]string{"team-a": "0"})

		var (
			pos   int
			saved = make(chan struct{}, 1)
		)
		// Every write moves a position, so the tick after it has
		// something to record rather than an unchanged state to skip.
		h.store.onSave = func() {
			pos++
			h.reporter.advance("team-a", strconv.Itoa(pos))

			select {
			case saved <- struct{}{}:
			default:
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		start := time.Now()

		go h.cp.Run(ctx)

		// The clock moves only while every goroutine is blocked, so a chan
		// read costs a tick rather than an interval, and the time it reports
		// back is exactly the interval that was waited for.
		<-saved
		if elapsed := time.Since(start); elapsed != interval {
			t.Errorf("got the first write after %s, want %s", elapsed, interval)
		}
		<-saved

		cancel()
		h.cp.Wait()

		if n := len(h.store.attempts()); n != 2 {
			t.Errorf("got %d writes, want one per tick", n)
		}
		if got := h.metrics.success.get(); got != 2 {
			t.Errorf("got %v successful writes, want 2", got)
		}
	})
}

func TestWaitOrdersFinalWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, time.Second)
		h.reporter.set(map[string]string{"team-a": "42"})

		var (
			writing = make(chan struct{})
			release = make(chan struct{})
		)
		h.store.onSave = sync.OnceFunc(func() {
			close(writing)
			<-release
		})
		ctx, cancel := context.WithCancel(context.Background())
		go h.cp.Run(ctx)

		// The save callback is blocked until it is released, so the
		// ticker is stopped below while a write is still in flight,
		// which the final one must not overlap.
		<-writing
		cancel()

		waited := make(chan struct{})
		go func() {
			h.cp.Wait()
			close(waited)
		}()
		synctest.Wait()

		select {
		case <-waited:
			t.Fatal("wait ended early while a write was still in flight")
		default:
		}
		close(release)
		<-waited

		// The final write runs once nothing else can write concurrently.
		h.reporter.advance("team-a", "43")
		h.cp.Write(context.Background())

		state := lastAttempt(t, h.store, 2)
		if want := map[string]string{"team-a": "43"}; !maps.Equal(state.Watches, want) {
			t.Errorf("got watches %v, want %v", state.Watches, want)
		}
	})
}

type harness struct {
	cp       *Checkpointer
	store    *fakeStore
	reporter *fakeReporter
	metrics  *metricsRecorder
	logs     *logRecorder
}

func newHarness(t *testing.T, interval time.Duration) *harness {
	t.Helper()

	var (
		store    = &fakeStore{}
		reporter = &fakeReporter{positions: make(map[string]string)}
		metrics  = newMetricsRecorder()
		logs     = &logRecorder{}
	)
	cfg := Config{SaveInterval: units.Duration(interval)}

	return &harness{
		cp:       NewCheckpointer(store, reporter, cfg, metrics.metrics(), logs.logger()),
		store:    store,
		reporter: reporter,
		metrics:  metrics,
		logs:     logs,
	}
}

func lastAttempt(t *testing.T, store *fakeStore, n int) State {
	t.Helper()

	states := store.attempts()
	if len(states) != n {
		t.Fatalf("got %d write attempts, want %d", len(states), n)
	}
	return states[len(states)-1]
}
