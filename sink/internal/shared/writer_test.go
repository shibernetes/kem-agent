package shared

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/shibernetes/kem-agent/sink"
)

func TestNewOpensNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	_ = NewWriter(path, 0o600)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file was created before Open was called: %v", err)
	}
}

func TestOpenCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	_ = openWriter(t, path, 0o600)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("got mode %o, want 600", got)
	}
}

// TestOpenKeepsModeOfExistingFile asserts that the mode applies
// only to a file the writer creates.
func TestOpenKeepsModeOfExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	_ = openWriter(t, path, 0o644)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("got mode %o, want the existing mode 600", got)
	}
}

func TestOpenRequiresDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "events.jsonl")

	if err := NewWriter(path, 0o600).Open(); err == nil {
		t.Error("open succeeded with no directory, want it to fail")
	}
}

func TestOpenRejectsWriterWithoutPath(t *testing.T) {
	cases := map[string]*Writer{
		"empty path": NewWriter("", 0o600),
		"stdout":     Stdout(),
	}
	for name, w := range cases {
		t.Run(name, func(t *testing.T) {
			if err := w.Open(); err == nil {
				t.Error("open succeeded, want it rejected")
			}
		})
	}
}

func TestOpenHandlesRotation(t *testing.T) {
	var (
		path = filepath.Join(t.TempDir(), "events.jsonl")
		w    = openWriter(t, path, 0o600)
	)
	write(t, w, "before")

	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("failed to rotate: %v", err)
	}
	if err := w.Open(); err != nil {
		t.Fatalf("failed to reopen: %v", err)
	}
	write(t, w, "after")

	cases := map[string]string{
		path:        "after",
		path + ".1": "before",
	}
	for name, want := range cases {
		t.Run(filepath.Base(name), func(t *testing.T) {
			if got := readFile(t, name); got != want+"\n" {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestOpenClosesReplacedFile(t *testing.T) {
	var (
		path     = filepath.Join(t.TempDir(), "events.jsonl")
		w        = openWriter(t, path, 0o600)
		replaced = w.file
	)
	if err := w.Open(); err != nil {
		t.Fatalf("failed to reopen: %v", err)
	}
	if _, err := replaced.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Errorf("got %v writing to the replaced file, want it closed", err)
	}
}

// TestOpenFailureKeepsFile asserts that a rotation whose open
// fails leaves the writer delivering to the file it already holds.
func TestOpenFailureKeepsFile(t *testing.T) {
	var (
		dir  = t.TempDir()
		path = filepath.Join(dir, "events.jsonl")
		w    = openWriter(t, path, 0o600)
	)
	// Removing the directory ensure that reopening fails, since
	// the file would be otherwise recreated using O_CREATE.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("failed to remove directory: %v", err)
	}
	if err := w.Open(); err == nil {
		t.Fatal("reopen succeeded with no directory, want it to fail")
	}
	if err := w.Write([]byte("still writing\n")); err != nil {
		t.Errorf("failed to write after a failed reopen: %v", err)
	}
}

func TestWriteWithNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")

	closed := openWriter(t, path, 0o600)
	closeWriter(t, closed)

	cases := map[string]struct {
		writer *Writer
		want   error
	}{
		"before open": {NewWriter(path, 0o600), sink.ErrNotOpen},
		"after close": {closed, sink.ErrClosed},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.writer.Write([]byte("line\n"))
			if err == nil {
				t.Fatal("the write succeeded, want it refused")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	var (
		path = filepath.Join(t.TempDir(), "events.jsonl")
		w    = openWriter(t, path, 0o600)
	)
	for range 3 {
		if err := w.Close(); err != nil {
			t.Fatalf("failed to close: %v", err)
		}
	}
}

// TestCloseKeepsStandardOutput asserts that closing a standard output
// writer leaves the stream open, since it belongs to the process.
func TestCloseKeepsStandardOutput(t *testing.T) {
	w := Stdout()
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}
	if w.file != os.Stdout {
		t.Error("the stream was closed, want it left open")
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		err   error
		wrote int
		want  sink.Result
	}{
		"denied":         {syscall.EACCES, 0, sink.ResultPermanent},
		"read-only":      {syscall.EROFS, 0, sink.ResultPermanent},
		"broken pipe":    {syscall.EPIPE, 0, sink.ResultPermanent},
		"interrupted":    {syscall.EINTR, 0, sink.ResultRetryable},
		"already closed": {os.ErrClosed, 0, sink.ResultPermanent},
		"no space left":  {syscall.ENOSPC, 0, sink.ResultRetryable},
		"partial write":  {syscall.ENOSPC, 512, sink.ResultPermanent},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := sink.Classify(classify(tc.err, tc.wrote)); got != tc.want {
				t.Errorf("got result %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWriteDuringOpen asserts that a rotation arriving
// mid-write is serialized against it.
func TestWriteDuringOpen(t *testing.T) {
	var (
		path = filepath.Join(t.TempDir(), "events.jsonl")
		w    = openWriter(t, path, 0o600)
	)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 50 {
			if err := w.Write([]byte("line\n")); err != nil {
				t.Errorf("failed to write: %v", err)
				return
			}
		}
	})
	wg.Go(func() {
		for range 50 {
			if err := w.Open(); err != nil {
				t.Errorf("failed to reopen: %v", err)
				return
			}
		}
	})
	wg.Wait()
}

// openWriter returns a writer with the file already open.
func openWriter(t *testing.T, path string, mode os.FileMode) *Writer {
	t.Helper()

	w := NewWriter(path, mode)
	if err := w.Open(); err != nil {
		t.Fatalf("failed to open: %v", err)
	}
	t.Cleanup(func() {
		_ = w.Close()
	})
	return w
}

func closeWriter(t *testing.T, w *Writer) {
	t.Helper()

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}
}

func write(t *testing.T, w *Writer, line string) {
	t.Helper()

	if err := w.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("failed to write: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", filepath.Base(path), err)
	}
	return string(b)
}
