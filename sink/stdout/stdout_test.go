package stdout

import (
	"errors"
	"testing"

	"github.com/shibernetes/kem-agent/sink"
)

func TestName(t *testing.T) {
	if got := New("console").Name(); got != "console" {
		t.Errorf("got %q, want %q", got, "console")
	}
}

func TestSinkHasEncoderAndFramer(t *testing.T) {
	s := New("console")

	if s.Encoder() == nil {
		t.Error("the sink has no encoder")
	}
	if s.Framer() == nil {
		t.Error("the sink has no framer")
	}
}

// TestSendRefusesNothing asserts that standard output accepts whatever
// it is given, so no record is ever reported as refused. The payload is
// empty to keep the test from writing to the terminal.
func TestSendRefusesNothing(t *testing.T) {
	s := New("console")

	t.Cleanup(func() {
		_ = s.Shutdown(t.Context())
	})
	refused, err := s.Send(t.Context(), nil, sink.NewBatchID())
	if err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if refused != 0 {
		t.Errorf("got %d refused records, want 0", refused)
	}
}

// TestSendAfterShutdown asserts that the sink stops delivering once
// it is shut down, even though the stream it wrote to stays open.
func TestSendAfterShutdown(t *testing.T) {
	s := New("console")

	if err := s.Shutdown(t.Context()); err != nil {
		t.Fatalf("failed to shut down: %v", err)
	}
	_, err := s.Send(t.Context(), nil, sink.NewBatchID())
	if !errors.Is(err, sink.ErrClosed) {
		t.Errorf("got %v, want the sink reported as closed", err)
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	s := New("console")

	for range 3 {
		if err := s.Shutdown(t.Context()); err != nil {
			t.Fatalf("failed to shut down: %v", err)
		}
	}
}
