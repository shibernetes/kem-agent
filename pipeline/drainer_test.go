package pipeline

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/sink"
)

func TestDrainerBatchesOnMaxEvents(t *testing.T) {
	cfg := testConfig()
	cfg.Batch.MaxEvents = 2

	var (
		s    = newFakeBatchSink()
		d, _ = newTestDrainer(t, s, cfg)
	)
	push(t, d, 3, 10)
	drain(t, d)

	if got := s.framer.composed(); !slices.Equal(got, []int{2, 1}) {
		t.Errorf("got batches of %v frames, want a full one then the remainder", got)
	}
}

func TestDrainerBatchesOnMaxBytes(t *testing.T) {
	s := &fakeBatchSink{framer: &fakeFramer{
		sep:    []byte("|"),
		prefix: []byte("<<<<<<<<<<"),
		suffix: []byte(">>>>>>>>>>"),
	}}
	cfg := testConfig()

	// A batch accounts for the frames, a separator between each pair,
	// and the twenty byte envelope. Two frames of ten leave eight of
	// the fifty allowed, so a third does not fit. Without the envelope
	// counted, four would.
	cfg.Batch.MaxBytes = 50
	d, _ := newTestDrainer(t, s, cfg)

	push(t, d, 4, 10)
	drain(t, d)

	if got := s.framer.composed(); !slices.Equal(got, []int{2, 2}) {
		t.Errorf("got batches of %v frames, want two of two", got)
	}
	const want = 20 + 10 + 1 + 10

	if got := len(s.payloads()[0]); got != want {
		t.Errorf("got a payload of %d bytes, want the envelope, two frames and a separator, so %d bytes",
			got, want)
	}
}

func TestDrainerBatchesOnLinger(t *testing.T) {
	// The linger timeout is the only limit a lone small frame
	// reaches, and it is waited out on the bubble's fake clock
	// rather than in real time.
	synctest.Test(t, func(t *testing.T) {
		var (
			cfg  = testConfig()
			s    = newFakeBatchSink()
			d, _ = newTestDrainer(t, s, cfg)
		)
		go d.Run()
		push(t, d, 1, 10)

		// The clock only moves once every goroutine is blocked, so the test
		// sleeps past the linger timeout rather than waiting on the batch.
		time.Sleep(time.Duration(cfg.Batch.Timeout))
		synctest.Wait()

		if got := len(s.payloads()); got != 1 {
			t.Errorf("got %d batches delivered, want the linger timeout to close one", got)
		}
		stop(t, d)
	})
}

func TestDrainerShipsOversizedFrameAlone(t *testing.T) {
	cfg := testConfig()
	cfg.Batch.MaxBytes = 100

	var (
		s    = newFakeBatchSink()
		d, _ = newTestDrainer(t, s, cfg)
	)
	// A frame larger than the batch limit ships alone.
	// Held to that limit, it would otherwise never be sent.
	push(t, d, 1, 500)
	push(t, d, 1, 10)
	drain(t, d)

	if got := s.framer.composed(); !slices.Equal(got, []int{1, 1}) {
		t.Fatalf("got batches of %v frames, want each frame sent alone", got)
	}
	if got := len(s.payloads()[0]); got != 500 {
		t.Errorf("got a first payload of %d bytes, want the 500 byte frame", got)
	}
}

// newTestDrainer returns a drainer over the given sink, and
// the recorder its instruments write to.
func newTestDrainer(t *testing.T, s *fakeBatchSink, cfg sink.DrainerConfig) (*Drainer, *recorder) {
	t.Helper()

	r := newRecorder()
	d := NewDrainer(s, cfg, r.metrics(), slog.New(slog.DiscardHandler))

	return d, r
}

// push queues n frames of the given size.
func push(t *testing.T, d *Drainer, n, size int) {
	t.Helper()

	p := d.Producer()
	var buf []byte
	for range n {
		buf = p.Produce(buf, testEvent(size))
	}
}

// drain runs the drainer until everything queued has been delivered.
// Queueing before it starts keeps a test free of any wait.
func drain(t *testing.T, d *Drainer) {
	t.Helper()

	drainWithin(t, d, 5*time.Second)
}

func drainWithin(t *testing.T, d *Drainer, timeout time.Duration) {
	t.Helper()

	go d.Run()
	stopWithin(t, d, timeout)
}

// stop ends the drainer and fails the test unless it drains in time.
func stop(t *testing.T, d *Drainer) {
	t.Helper()

	stopWithin(t, d, 5*time.Second)
}

func stopWithin(t *testing.T, d *Drainer, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := d.Stop(ctx); err != nil {
		t.Fatalf("drain did not finish in time: %v", err)
	}
}

func testConfig() sink.DrainerConfig {
	return sink.DrainerConfig{
		Batch: sink.BatchConfig{
			MaxEvents: 512,
			MaxBytes:  1 << 20,
			Timeout:   units.Duration(50 * time.Millisecond),
		},
		Queue:       sink.QueueConfig{MaxBytes: sink.QueueChunkSize},
		Retry:       sink.RetryConfig{Enabled: false},
		SendTimeout: units.Duration(time.Second),
	}
}
