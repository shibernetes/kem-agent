package pipeline

import (
	"testing"

	"github.com/shibernetes/kem-agent/sink"
)

func TestDirectProducerDelivers(t *testing.T) {
	var (
		s   = &fakeEventSink{}
		r   = newRecorder()
		buf = make([]byte, 0, 8)
	)
	got := NewDirectProducer(s, r.metrics()).Produce(buf, testEvent(3))

	if n := len(s.handled()); n != 1 {
		t.Fatalf("got %d events handled, want 1", n)
	}
	if n := r.delivered.get(); n != 1 {
		t.Errorf("got %v events delivered, want 1", n)
	}
	// Nothing is encoded, so the buffer comes back untouched.
	if cap(got) != cap(buf) {
		t.Errorf("got a buffer of capacity %d, want the original %d", cap(got), cap(buf))
	}
}

func TestDirectProducerRecordsRefusal(t *testing.T) {
	s := &fakeEventSink{err: sink.ErrRejected}
	r := newRecorder()

	NewDirectProducer(s, r.metrics()).Produce(nil, testEvent(3))

	if n := r.rejected.get(); n != 1 {
		t.Errorf("got %v events rejected, want 1", n)
	}
	if n := r.delivered.get(); n != 0 {
		t.Errorf("got %v events delivered, want none", n)
	}
}

func TestBatchProducerQueuesFrame(t *testing.T) {
	d, _ := newTestDrainer(t, newFakeBatchSink(), testConfig())

	d.Producer().Produce(nil, testEvent(64))

	if got := d.queue.Len(); got != 1 {
		t.Fatalf("got %d frames queued, want 1", got)
	}
	if got := d.queue.Usage(); got != 64 {
		t.Errorf("got %d bytes queued, want 64", got)
	}
}

func TestBatchProducerReturnsGrownBuffer(t *testing.T) {
	d, _ := newTestDrainer(t, newFakeBatchSink(), testConfig())

	// Encoding a frame past the buffer's capacity grows it,
	// and a caller discarding the result would grow it again
	// on every event.
	got := d.Producer().Produce(make([]byte, 0, 8), testEvent(512))

	if cap(got) < 512 {
		t.Fatalf("got a buffer of capacity %d, want it large enough to hold the frame", cap(got))
	}
	if len(got) != 512 {
		t.Errorf("got %d bytes back, want the 512 byte frame", len(got))
	}
}

func TestBatchProducerRecordsTooLarge(t *testing.T) {
	cfg := testConfig()
	d, r := newTestDrainer(t, newFakeBatchSink(), cfg)

	// A frame larger than the whole queue cannot be held.
	d.Producer().Produce(nil, testEvent(int(cfg.Queue.MaxBytes)+1))

	if n := r.tooLarge.get(); n != 1 {
		t.Errorf("got %v events refused, want 1", n)
	}
	if got := d.queue.Len(); got != 0 {
		t.Errorf("got %d frames queued, want none", got)
	}
}

func TestBatchProducerRecordsOverflow(t *testing.T) {
	d, r := newTestDrainer(t, newFakeBatchSink(), testConfig())
	p := d.Producer()

	// Four frames of ten thousand bytes overrun a queue
	// of one chunk, so the fourth gives up the first.
	var scratch []byte
	for range 4 {
		scratch = p.Produce(scratch, testEvent(10_000))
	}
	if n := r.overflow.get(); n != 1 {
		t.Errorf("got %v frames evicted, want 1", n)
	}
	if got := d.queue.Len(); got != 3 {
		t.Errorf("got %d frames queued, want 3", got)
	}
}
