package source

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// TestWatcherExpiryKeepsPositionUntilReplayEnds asserts that an expired
// resource version is counted but not logged, and stays recorded while the
// replay runs. A checkpoint written during the replay would otherwise store
// no resource version for the namespace, and a restart would replay every
// event the APIServer retains.
func TestWatcherExpiryKeepsPositionUntilReplayEnds(t *testing.T) {
	var (
		m = newMetricsRecorder()
		r = &logRecorder{}
		w = newTestWatcher(t, watcherOptions{
			config:  WatchConfig{Namespace: testNamespace},
			metrics: m.metrics(),
			logger:  r.logger(),
			resume:  "1000",
		})
	)
	w.expire()

	if got := m.counts()["expired"]; got != 1 {
		t.Errorf("got %v expirations counted, want 1", got)
	}
	if got := r.count(); got != 0 {
		t.Errorf("got %d lines, want the expiry counted and not logged", got)
	}
	if err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Add(watchedEvent("1042"))
	}); err != nil {
		t.Fatalf("the stream ended with an error: %v", err)
	}
	if got := w.position(); got != "1000" {
		t.Fatalf("got position %q during the replay, want the expired one retained", got)
	}
	if err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Action(watch.Bookmark, bookmarkFrame("1100", true))
	}); err != nil {
		t.Fatalf("the stream ended with an error: %v", err)
	}
	if got := w.position(); got != "1100" {
		t.Errorf("got position %q, want the bookmark ending the replay", got)
	}
}

// TestWatcherExpiryReplaysAfterPosition asserts that the replay after an
// expired position delivers only the events written after it.
func TestWatcherExpiryReplaysAfterPosition(t *testing.T) {
	var (
		r = &eventRecorder{}
		w = newTestWatcher(t, watcherOptions{
			config:     WatchConfig{Namespace: testNamespace},
			dispatcher: r,
			resume:     "1000",
		})
	)
	w.expire()

	if err := drain(t, w, func(s *watch.FakeWatcher) {
		s.Add(watchedEvent("998"))
		s.Add(watchedEvent("1000"))
		s.Add(watchedEvent("1042"))
		s.Action(watch.Bookmark, bookmarkFrame("1100", true))
	}); err != nil {
		t.Fatalf("the stream ended with an error: %v", err)
	}
	names := r.names()
	if len(names) != 1 {
		t.Fatalf("got %d events dispatched, want only the one with a newer resource version", len(names))
	}
	if names[0] != "event-1042" {
		t.Errorf("got event %q, want %q", names[0], "event-1042")
	}
}

func TestWatcherLogsReconnect(t *testing.T) {
	var (
		r  = &logRecorder{}
		fw = newFakeWatch(errors.New("connection refused")).withFrames(
			func(*watch.FakeWatcher) {},
			func(*watch.FakeWatcher) {},
		)
		w = newTestWatcher(t, watcherOptions{
			client: fw.clientset(),
			config: WatchConfig{Namespace: testNamespace},
			logger: r.logger(),
			resume: "1000",
		})
	)
	if err := w.watchOnce(t.Context()); !errors.Is(err, errRetryable) {
		t.Fatalf("got %v, want %v", err, errRetryable)
	}
	// The first reconnects after the failure, the second after a clean end.
	for range 2 {
		if err := w.watchOnce(t.Context()); err != nil {
			t.Fatalf("the stream ended with an error: %v", err)
		}
	}
	want := []string{
		"watch failed, retrying",
		"watch reconnected",
	}
	if got := r.messages(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestWatcherRunBackoff asserts that a failing watch reconnects with
// an exponential backoff. The backoff delay is jittered, so each wait
// duration falls in a range rather than on an exact value.
func TestWatcherRunBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			fw = newFakeWatch(failing(5)...)
			w  = newTestWatcher(t, watcherOptions{
				client: fw.clientset(),
				config: WatchConfig{Namespace: testNamespace},
				resume: "1000",
			})
		)
		runWatcher(t, w)

		// Sleep long enough for the four backoff delays
		// asserted below, 22.5s at worst.
		time.Sleep(time.Minute)
		synctest.Wait()

		intervals := fw.reconnectIntervals()

		for i, factor := range []int{1, 2, 4, 8} {
			want := time.Duration(factor) * minRestartDelay
			if i >= len(intervals) {
				t.Fatalf("the watch reconnected %d times, want at least 5", len(intervals))
			}
			if intervals[i] < want || intervals[i] >= want+want/2 {
				t.Errorf("got %v delay before attempt %d, want it within [%v, %v)",
					intervals[i], i+2, want, want+want/2)
			}
		}
	})
}

func TestWatcherRunStopsOnCancel(t *testing.T) {
	cases := map[string]func() *fakeWatch{
		"while reading a stream":     func() *fakeWatch { return newFakeWatch() },
		"while waiting to reconnect": func() *fakeWatch { return newFakeWatch(failing(1)...) },
	}
	for name, newFake := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newTestWatcher(t, watcherOptions{
					client: newFake().clientset(),
					config: WatchConfig{Namespace: testNamespace},
					resume: "1000",
				})
				h := runWatcher(t, w)
				synctest.Wait()

				if err := h.stop(); err != nil {
					t.Errorf("got %v, want the watch to stop cleanly", err)
				}
			})
		})
	}
}

// TestWatcherRunCountsRestarts asserts that each way a stream can
// end is counted under its own reason.
func TestWatcherRunCountsRestarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			m  = newMetricsRecorder()
			fw = newFakeWatch(nil, errors.New("connection refused")).withFrames(
				func(*watch.FakeWatcher) {},
				func(s *watch.FakeWatcher) { s.Error(goneStatus()) },
			)
			w = newTestWatcher(t, watcherOptions{
				client:  fw.clientset(),
				config:  WatchConfig{Namespace: testNamespace},
				metrics: m.metrics(),
				resume:  "1000",
			})
		)
		runWatcher(t, w)

		time.Sleep(30 * time.Second)
		synctest.Wait()

		want := map[string]float64{
			"closed":  1,
			"error":   1,
			"expired": 1,
		}
		if diff := cmp.Diff(want, m.counts()); diff != "" {
			t.Errorf("the restarts differ (-want +got):\n%s", diff)
		}
	})
}

// TestWatcherRunStopsOnRefusedStreamingList asserts that a watch
// refused because of the streaming list options stops the agent
// instead of retrying.
func TestWatcherRunStopsOnRefusedStreamingList(t *testing.T) {
	var (
		fw = newFakeWatch(invalidError(metav1.CauseTypeForbidden, "sendInitialEvents"))
		w  = newTestWatcher(t, watcherOptions{
			client: fw.clientset(),
			config: WatchConfig{Namespace: testNamespace},
		})
	)
	err := runWatcher(t, w).result()
	if err == nil {
		t.Fatal("run returned no error, want the refusal reported")
	}
	if !strings.Contains(err.Error(), testNamespace) {
		t.Errorf("got %q, want it to name the namespace", err)
	}
	if got := fw.calls(); got != 1 {
		t.Errorf("got %d attempts, want no retry", got)
	}
}

// TestWatcherReconnectsAfterCleanClose asserts that a watch closed
// periodically by the APIServer is reopened at once.
func TestWatcherReconnectsAfterCleanClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			fw = newFakeWatch()
			w  = newTestWatcher(t, watcherOptions{
				client: fw.clientset(),
				config: WatchConfig{Namespace: testNamespace},
				resume: "1000",
			})
		)
		runWatcher(t, w)
		synctest.Wait()

		lifetime := 2 * minRestartDelay
		time.Sleep(lifetime)
		fw.stream(t, 0).Stop()
		synctest.Wait()

		if got := fw.calls(); got != 2 {
			t.Fatalf("the watch was opened %d times, want it reopened", got)
		}
		if got := fw.reconnectIntervals()[0]; got != lifetime {
			t.Errorf("got %v delay between streams, want %v with no backoff", got, lifetime)
		}
	})
}

// TestWatcherStopsWhileConnecting asserts that a connection failing
// while the agent is shutting down is not reported.
func TestWatcherStopsWhileConnecting(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	w := newTestWatcher(t, watcherOptions{
		client: newFakeWatch(failing(1)...).clientset(),
		config: WatchConfig{Namespace: testNamespace},
		resume: "1000",
	})
	if err := w.watchOnce(ctx); err != nil {
		t.Errorf("got %v, want a canceled connection to stop cleanly", err)
	}
}

// failing returns n connection failures, one per watch call.
func failing(n int) []error {
	errs := make([]error, n)
	for i := range errs {
		errs[i] = errors.New("connection refused")
	}
	return errs
}

// runWatcher starts a watcher and stops it on cleanup.
// A Fatal that leaves a watcher running inside a bubble panics with
// a deadlock and reports the assertion nowhere, so the stop is not
// left to the caller.
func runWatcher(t *testing.T, w *namespaceWatcher) *watcherHandle {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h := &watcherHandle{cancel: cancel, err: make(chan error, 1)}
	go func() {
		h.err <- w.run(ctx)
	}()
	return h
}

type watcherHandle struct {
	cancel context.CancelFunc
	err    chan error
}

// stop ends the run and returns what it reported.
func (h *watcherHandle) stop() error {
	h.cancel()
	return <-h.err
}

// result returns what the run reported, for a watcher stopping on its own.
func (h *watcherHandle) result() error {
	return <-h.err
}
