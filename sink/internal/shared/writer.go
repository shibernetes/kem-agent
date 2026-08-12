package shared

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"syscall"

	"github.com/shibernetes/kem-agent/sink"
)

const (
	openFlags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
)

// A Writer serializes writes to a file from multiple callers.
type Writer struct {
	path   string
	mode   fs.FileMode
	file   *os.File
	closed bool
	mu     sync.Mutex
}

// NewWriter returns a writer for the file at path. The file is not
// opened until Open is called, so building a writer performs no I/O.
func NewWriter(path string, mode fs.FileMode) *Writer {
	return &Writer{path: path, mode: mode}
}

// Stdout returns a writer for the process standard output.
func Stdout() *Writer {
	return &Writer{file: os.Stdout}
}

// Open opens the file, creating it if necessary, but never the directory
// holding it. The mode applies only to a file that is created. Open can
// be called again to pick up a rotated file.
func (w *Writer) Open() error {
	if w.path == "" {
		return errors.New("cannot open a writer that has no path")
	}
	// Open without holding the lock, to avoid blocking
	// writes to the current file.
	file, err := os.OpenFile(w.path, openFlags, w.mode)
	if err != nil {
		return fmt.Errorf("failed to open %q: %w", w.path, err)
	}
	w.mu.Lock()
	old := w.file
	w.file = file
	w.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	return nil
}

// Write writes len(p) bytes from p to the file. An error returned
// after any bytes were written is permanent, since retrying would
// write twice.
func (w *Writer) Write(p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	switch {
	case w.closed:
		return fmt.Errorf("%w: %s", sink.ErrClosed, w.destination())
	case w.file == nil:
		return fmt.Errorf("%w: %s", sink.ErrNotOpen, w.destination())
	}
	var wrote int
	for wrote < len(p) {
		n, err := w.file.Write(p[wrote:])
		wrote += n
		if err != nil {
			return fmt.Errorf("failed to write: %w", classify(err, wrote))
		}
	}
	return nil
}

// Close closes the file.
// It is a no-op if the file is already closed.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.closed = true

	if w.file == nil || w.path == "" {
		// Standard output is never closed.
		return nil
	}
	file := w.file
	w.file = nil

	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close %q: %w", w.path, err)
	}
	return nil
}

func (w *Writer) destination() string {
	if w.path == "" {
		return "stdout"
	}
	return w.path
}

// classify maps a write failure to a delivery outcome.
// A partial write is permanent, whatever the error is.
func classify(err error, wrote int) error {
	switch {
	case wrote > 0:
		return fmt.Errorf("%w: wrote %d bytes of the payload: %w", sink.ErrPermanent, wrote, err)
	case errors.Is(err, syscall.EACCES),
		errors.Is(err, syscall.EROFS),
		errors.Is(err, syscall.EPIPE),
		errors.Is(err, os.ErrClosed):
		return fmt.Errorf("%w: %w", sink.ErrPermanent, err)
	default:
		// ENOSPC falls to the default, since space can free up.
		return err
	}
}
