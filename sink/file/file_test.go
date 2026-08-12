package file

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/sink"
)

func TestName(t *testing.T) {
	if got := New("archive", testConfig("/var/log/events.jsonl")).Name(); got != "archive" {
		t.Errorf("got %q, want %q", got, "archive")
	}
}

func TestSinkHasEncoderAndFramer(t *testing.T) {
	s := New("archive", testConfig("/var/log/events.jsonl"))

	if s.Encoder() == nil {
		t.Error("the sink has no encoder")
	}
	if s.Framer() == nil {
		t.Error("the sink has no framer")
	}
}

func TestSendWritesOneLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")

	s := openSink(t, path)
	mustSend(t, s, "container killed")

	got := readFile(t, path)
	if n := strings.Count(got, "\n"); n != 1 {
		t.Errorf("got %d lines, want 1", n)
	}
	if !strings.Contains(got, `"note":"container killed"`) {
		t.Errorf("got %q, want the event written to it", got)
	}
}

// TestSendBeforeOpen asserts that a sink whose file never opened refuses
// its batches outright, rather than retrying what cannot be delivered.
func TestSendBeforeOpen(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "events.jsonl"))

	_, err := send(t, New("archive", cfg), "dropped")
	if !errors.Is(err, sink.ErrNotOpen) {
		t.Errorf("got %v, want the sink reported as not open", err)
	}
}

// TestReopenFailureIsRecoverable asserts that a failed file reopen
// is reported and keeps the file opened, and that a later one picks
// up the rotated file.
func TestReopenFailureIsRecoverable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	path := filepath.Join(dir, "events.jsonl")

	s := openSink(t, path)
	mustSend(t, s, "before")

	// Removing the directory, and not just the file is what
	// makes the reopen fail since Open creates it again.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("failed to remove directory: %v", err)
	}
	if err := s.Open(t.Context()); err == nil {
		t.Fatal("the reopen succeeded with no directory, want it to fail")
	}
	// The file is unlinked rather than closed, so the sink keeps
	// delivering to the one it already holds.
	mustSend(t, s, "still delivering")

	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to reopen once the directory existed: %v", err)
	}
	mustSend(t, s, "after")

	if got := readFile(t, path); !strings.Contains(got, "after") {
		t.Errorf("got %q, want the event written to the new file", got)
	}
}

func TestOpenPicksUpRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")

	s := openSink(t, path)
	mustSend(t, s, "before")

	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("failed to rotate: %v", err)
	}
	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to reopen: %v", err)
	}
	mustSend(t, s, "after")

	cases := map[string]string{
		path:        "after",
		path + ".1": "before",
	}
	for name, want := range cases {
		t.Run(filepath.Base(name), func(t *testing.T) {
			if got := readFile(t, name); !strings.Contains(got, want) {
				t.Errorf("got %q, want it to contain %q", got, want)
			}
		})
	}
}

// TestSendAfterShutdown asserts that a closed sink refuses the
// batches it is given rather than writing to a closed file.
func TestSendAfterShutdown(t *testing.T) {
	s := openSink(t, filepath.Join(t.TempDir(), "events.jsonl"))
	if err := s.Shutdown(t.Context()); err != nil {
		t.Fatalf("failed to shut down: %v", err)
	}
	_, err := send(t, s, "too late")
	if !errors.Is(err, sink.ErrClosed) {
		t.Errorf("got %v, want the sink reported as closed", err)
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	s := openSink(t, filepath.Join(t.TempDir(), "events.jsonl"))

	for range 3 {
		if err := s.Shutdown(t.Context()); err != nil {
			t.Fatalf("failed to shut down: %v", err)
		}
	}
}

// openSink returns a sink with its file already open.
func openSink(t *testing.T, path string) *Sink {
	t.Helper()

	s := New("archive", testConfig(path))
	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to open: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown(t.Context())
	})
	return s
}

func send(t *testing.T, s *Sink, note string) (int, error) {
	t.Helper()

	frame := s.Encoder().AppendEvent(nil, &event.Event{Note: note})
	return s.Send(t.Context(), s.Framer().Compose(nil, frame, 1), sink.NewBatchID())
}

func mustSend(t *testing.T, s *Sink, note string) {
	t.Helper()

	refused, err := send(t, s, note)
	if err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if refused != 0 {
		t.Errorf("got %d refused records, want 0", refused)
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
