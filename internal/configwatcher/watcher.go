package configwatcher

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// pollInterval re-checks the files on a fixed schedule as fallback for
	// content changes fsnotify does not report, such as a dropped event or
	// an edit of a symlink target outside a watched directory.
	pollInterval = 30 * time.Second

	// debounceInterval coalesces a burst of filesystem events that a single
	// change produces, most notably the atomic symlink swap Kubernetes does
	// when it updates the content of a mounted volume.
	debounceInterval = 2 * time.Second
)

// A Watcher notifies when the content of the files it watches changes.
// Any event that leaves their content unchanged is ignored.
type Watcher struct {
	paths    []string
	dirs     []string
	callback func()
	hashes   []uint64
}

// New builds a Watcher for the files at paths, watched together so that
// a change impacting several of them fires once. The callback runs on
// the watch goroutine on each observed change and must be non-blocking.
func New(callback func(), paths ...string) (*Watcher, error) {
	if len(paths) == 0 {
		return nil, errors.New("at least one path is required")
	}
	w := &Watcher{callback: callback}

	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve path %q: %w", path, err)
		}
		w.paths = append(w.paths, abs)

		// A TLS keypair usually shares one directory, and fsnotify
		// reports every event in it whichever path added it.
		if dir := filepath.Dir(abs); !slices.Contains(w.dirs, dir) {
			w.dirs = append(w.dirs, dir)
		}
	}
	return w, nil
}

// Watch watches the files until ctx is canceled.
func (w *Watcher) Watch(ctx context.Context, logger *slog.Logger) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create filesystem watcher: %w", err)
	}
	defer func() {
		_ = fsw.Close()
	}()
	for _, dir := range w.dirs {
		if err := fsw.Add(dir); err != nil {
			return fmt.Errorf("failed to add watch for dir %q: %w", dir, err)
		}
	}
	return w.run(ctx, logger, fsw.Events, fsw.Errors)
}

// run runs the watch loop.
// It takes the events chan produced by fsnotify rather than reading
// them directly, so it can run against a fake clock in tests.
func (w *Watcher) run(ctx context.Context, logger *slog.Logger, events <-chan fsnotify.Event, errs <-chan error) error {
	hashes, err := w.read()
	if err != nil {
		return err
	}
	w.hashes = hashes
	logger.LogAttrs(ctx, slog.LevelInfo, "files watched", slog.Any("paths", w.paths))

	timer := time.NewTimer(debounceInterval)
	timer.Stop()
	defer timer.Stop()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	check := func() {
		if e := w.checkContent(); e != nil {
			logger.LogAttrs(ctx, slog.LevelWarn, "change detection failed", slog.Any("error", e))
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-events:
			if !ok {
				return nil
			}
			// Any event in a watched directory opens a debounce window, rather
			// than only one naming a watched file. A mounted volume is updated
			// by renaming a symlink over ..data, which kqueue does not report
			// at all, and the content hash is what signals whether any file
			// actually changed.
			timer.Reset(debounceInterval)
		case e, ok := <-errs:
			if !ok {
				return nil
			}
			logger.LogAttrs(ctx, slog.LevelWarn, "watch error", slog.Any("error", e))
		case <-timer.C:
			check()
		case <-ticker.C:
			check()
		}
	}
}

// read returns a hash of each watched file, index-aligned with the paths.
// Hashing them apart rather than as one stream keeps a byte moving from
// the end of one file to the start of the next from reading as no change.
func (w *Watcher) read() ([]uint64, error) {
	hashes := make([]uint64, len(w.paths))

	for i, path := range w.paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %q: %w", path, err)
		}
		h := fnv.New64a()
		_, _ = h.Write(b)
		hashes[i] = h.Sum64()
	}
	return hashes, nil
}

// checkContent fires the watcher's callback when the content
// of a watched file has changed since the last check.
func (w *Watcher) checkContent() error {
	hashes, err := w.read()
	if err != nil {
		return err
	}
	if slices.Equal(hashes, w.hashes) {
		return nil
	}
	w.hashes = hashes
	w.callback()

	return nil
}
