package configwatcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestNewRejectsNoPaths(t *testing.T) {
	if _, err := New(func() {}); err == nil {
		t.Error("got no error, want at least one path required")
	}
}

func TestNewDeduplicatesDirectories(t *testing.T) {
	dir := t.TempDir()
	w := mustNew(t, func() {}, filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))

	if len(w.paths) != 2 {
		t.Errorf("got %d paths, want 2", len(w.paths))
	}
	// A TLS keypair in one mount takes one fsnotify watch, so each
	// of its events arrives once rather than once per watched file.
	// Coalescing them into a single callback is the debounce's job.
	if len(w.dirs) != 1 {
		t.Errorf("got %d directories, want the shared one watched once", len(w.dirs))
	}
}

func TestCheckContentIgnoresUnchangedFile(t *testing.T) {
	calls, w := loaded(t, writeFile(t, t.TempDir(), "config.yaml", ""))

	if err := w.checkContent(); err != nil {
		t.Fatalf("failed to check content: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("got %d calls, want none for unchanged content", got)
	}
}

func TestCheckContentFiresOnChange(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "foo")
	calls, w := loaded(t, path)

	writeFile(t, filepath.Dir(path), "config.yaml", "bar")

	if err := w.checkContent(); err != nil {
		t.Fatalf("failed to check content: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("got %d calls, want 1", got)
	}
}

func TestRunDebouncesEvents(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "foo")

	synctest.Test(t, func(t *testing.T) {
		var (
			calls, w    = counting(t, path)
			ctx, cancel = context.WithCancel(context.Background())
			events      = make(chan fsnotify.Event)
			done        = startRun(t, ctx, w, events)
		)
		// The content differs at every step, so a loop that checked per
		// event rather than per window would report three changes.
		for _, content := range []string{
			"bar", "baz", "buz",
		} {
			writeFile(t, filepath.Dir(path), "config.yaml", content)
			events <- fsnotify.Event{Name: w.paths[0], Op: fsnotify.Write}
			time.Sleep(debounceInterval / 2)
		}
		time.Sleep(debounceInterval)
		synctest.Wait()

		if got := calls.Load(); got != 1 {
			t.Errorf("got %d calls, want the burst coalesced into 1", got)
		}
		cancel()
		<-done
	})
}

// A dropped inotify event leaves the file changed with nothing to report
// it, which is what the poll exists for.
func TestRunPollsForMissedEvents(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "a")

	synctest.Test(t, func(t *testing.T) {
		calls, w := counting(t, path)
		ctx, cancel := context.WithCancel(context.Background())
		done := startRun(t, ctx, w, make(chan fsnotify.Event))

		writeFile(t, filepath.Dir(path), "config.yaml", "b")
		time.Sleep(pollInterval + time.Second)
		synctest.Wait()

		if got := calls.Load(); got != 1 {
			t.Errorf("got %d calls, want the poll to catch the missed change", got)
		}
		cancel()
		<-done
	})
}

func TestRunReturnsOnCancel(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "a")

	synctest.Test(t, func(t *testing.T) {
		_, w := counting(t, path)
		ctx, cancel := context.WithCancel(context.Background())
		done := startRun(t, ctx, w, make(chan fsnotify.Event))

		// The bubble deadlocks rather than fails if the loop
		// keeps running, so the chan close asserts it finishes.
		cancel()
		<-done
	})
}

func mustNew(t *testing.T, callback func(), paths ...string) *Watcher {
	t.Helper()

	w, err := New(callback, paths...)
	if err != nil {
		t.Fatalf("failed to build watcher: %v", err)
	}
	return w
}

// counting builds a Watcher over paths that count its
// callback invocations.
func counting(t *testing.T, paths ...string) (*atomic.Int64, *Watcher) {
	t.Helper()

	var calls atomic.Int64
	return &calls, mustNew(t, func() { calls.Add(1) }, paths...)
}

// loaded builds a counting Watcher holding the hashes its
// loop would have read at startup.
func loaded(t *testing.T, paths ...string) (*atomic.Int64, *Watcher) {
	t.Helper()

	calls, w := counting(t, paths...)
	hashes, err := w.read()
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	w.hashes = hashes

	return calls, w
}

// startRun runs the watch loop in the caller's bubble,
// and returns a channel that is closed when it stops.
func startRun(t *testing.T, ctx context.Context, w *Watcher, events <-chan fsnotify.Event) <-chan struct{} {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := w.run(ctx, slog.New(slog.DiscardHandler), events, make(chan error)); err != nil {
			t.Errorf("got %v, want the loop to stop cleanly", err)
		}
	}()
	synctest.Wait()

	return done
}

// mountedDir builds a directory in the shape a kubelet mounts
// a ConfigMap or a Secret in, and returns its path.
func mountedDir(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	swapTimestampDir(t, dir, files)

	for name := range files {
		link := filepath.Join(dir, name)
		if err := os.Symlink(filepath.Join("..data", name), link); err != nil {
			t.Fatalf("failed to symlink %q: %v", link, err)
		}
	}
	return dir
}

// swapTimestampDir writes a new timestamped directory of the mounted
// files and moves ..data onto it, which is how the kubelet updates a
// volume without ever leaving a half-written file visible.
func swapTimestampDir(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	tsDir, err := os.MkdirTemp(dir, time.Now().UTC().Format("..2006_01_02_15_04_05."))
	if err != nil {
		t.Fatalf("failed to create timestamped dir in %q: %v", dir, err)
	}
	for name, content := range files {
		writeFile(t, tsDir, name, content)
	}
	tmp := filepath.Join(dir, "..data_tmp")

	if err := os.Symlink(filepath.Base(tsDir), tmp); err != nil {
		t.Fatalf("failed to symlink %q: %v", tmp, err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "..data")); err != nil {
		t.Fatalf("failed to swap %q: %v", tmp, err)
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write %q: %v", path, err)
	}
	return path
}
