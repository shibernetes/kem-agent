package pipeline

import (
	"testing"

	"github.com/shibernetes/kem-agent/sink"
)

func TestNewChunkIsClean(t *testing.T) {
	c := newChunk()
	if got := len(c.buf); got != sink.QueueChunkSize {
		t.Errorf("got a %d byte chunk, want %d", got, sink.QueueChunkSize)
	}
	if c.r != 0 || c.w != 0 {
		t.Errorf("got offsets r=%d w=%d, want both at zero", c.r, c.w)
	}
	if c.next != nil {
		t.Error("got a linked chunk, want it with no successor")
	}
}

func TestChunkReleaseClearsOffsets(t *testing.T) {
	c := newChunk()
	c.r, c.w = 69, 420
	c.release()

	// The next queue to take this chunk would start from the
	// offsets it was released with, reading frames it never wrote.
	if c.r != 0 || c.w != 0 {
		t.Errorf("got offsets r=%d w=%d, want both cleared", c.r, c.w)
	}
}

func TestChunkReleaseDetaches(t *testing.T) {
	c, next := newChunk(), newChunk()
	c.next = next
	c.release()

	// A pooled chunk still pointing at its successor holds the
	// whole chain behind it alive.
	if c.next != nil {
		t.Error("got a released linked chunk, want it with no successor")
	}
}

func TestChunkReleaseKeepsBytes(t *testing.T) {
	c := newChunk()
	c.buf[0], c.buf[len(c.buf)-1] = 0xBE, 0xEF
	c.release()

	// Clearing the buffer would cost a 32 KiB write per chunk to hide
	// bytes no read can reach, the write offset being reset to zero.
	if c.buf[0] != 0xBE || c.buf[len(c.buf)-1] != 0xEF {
		t.Error("got a zeroed buffer, want its bytes left alone")
	}
}

func TestChunkOffsets(t *testing.T) {
	cases := map[string]struct {
		r, w       int
		wantFree   int
		wantUnread int
	}{
		"fresh":          {0, 0, sink.QueueChunkSize, 0},
		"partly written": {0, 100, sink.QueueChunkSize - 100, 100},
		"partly read":    {40, 100, sink.QueueChunkSize - 100, 60},
		"drained":        {100, 100, sink.QueueChunkSize - 100, 0},
		"fully written":  {0, sink.QueueChunkSize, 0, sink.QueueChunkSize},
		"fully read":     {sink.QueueChunkSize, sink.QueueChunkSize, 0, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &chunk{buf: make([]byte, sink.QueueChunkSize), r: tc.r, w: tc.w}
			if got := c.free(); got != tc.wantFree {
				t.Errorf("got %d free bytes, want %d", got, tc.wantFree)
			}
			if got := c.unread(); got != tc.wantUnread {
				t.Errorf("got %d unread bytes, want %d", got, tc.wantUnread)
			}
		})
	}
}
