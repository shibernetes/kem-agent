package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/testutil"
	"github.com/shibernetes/kem-agent/sink"
)

// TestOpenReportsUnreadableCertificate asserts that a certificate file that
// can't be read fails when the sink opens rather than on the first produce.
func TestOpenReportsUnreadableCertificate(t *testing.T) {
	cfg := testConfig()
	cfg.TLS.CAFile = filepath.Join(t.TempDir(), "missing.crt")

	s := New("events", cfg, slog.New(slog.DiscardHandler), testAgentMetadata())
	if err := s.Open(t.Context()); err == nil {
		_ = s.Shutdown(context.Background())
		t.Fatal("the sink opened, want the missing certificate reported")
	}
}

// TestSendProducesOneRecordPerEvent asserts that every event is produced as a
// single record, in the order it was sent, and that sending a new batch doesn't
// send the previous batch's records again.
func TestSendProducesOneRecordPerEvent(t *testing.T) {
	var (
		c      = newFakeCluster(t, 1)
		s      = newTestSink(t, fakeConfig(c))
		events = testEvents(3)
	)
	for _, batch := range [][]*event.Event{events[:2], events[2:]} {
		refused, err := s.Send(t.Context(), encodeBatch(s, batch...), sink.NewBatchID())
		if err != nil {
			t.Fatalf("failed to send batch: %v", err)
		}
		if refused != 0 {
			t.Errorf("got %d records refused, want none", refused)
		}
	}
	records := consumeRecords(t, c, len(events))
	if len(records) != len(events) {
		t.Fatalf("got %d records, want %d", len(records), len(events))
	}
	headers := recordHeaders(testAgentMetadata())

	for i, ev := range events {
		r := records[i]
		if string(r.Key) != string(ev.UID) {
			t.Errorf("record %d has key %q, want %q", i, r.Key, ev.UID)
		}
		if want := canonicalJSON(ev); !bytes.Equal(r.Value, want) {
			t.Errorf("record %d has value %s, want %s", i, r.Value, want)
		}
		if !slices.EqualFunc(r.Headers, headers, equalHeaders) {
			t.Errorf("record %d has headers %v, want %v", i, r.Headers, headers)
		}
	}
}

// TestSendClassifiesBrokerErrors asserts how each kind of broker error ends a batch.
func TestSendClassifiesBrokerErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		code    *kerr.Error
		refused int
		want    sink.Result
		cause   error
	}{
		"record too large":     {kerr.MessageTooLarge, 2, sink.ResultSuccess, nil},
		"topic not authorized": {kerr.TopicAuthorizationFailed, 0, sink.ResultPermanent, kerr.TopicAuthorizationFailed},

		// The client fails with its record timeout error, not the broker's error.
		"not enough replicas": {kerr.NotEnoughReplicas, 0, sink.ResultRetryable, kgo.ErrRecordTimeout},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newFakeCluster(t, 1)
			failProduce(c, tc.code)

			cfg := fakeConfig(c)
			cfg.SendTimeout = minSendTimeout

			s := newTestSink(t, cfg)
			refused, err := s.Send(t.Context(), encodeBatch(s, testEvent(), testEvent()), sink.NewBatchID())

			if got := sink.Classify(err); got != tc.want {
				t.Fatalf("got %s from %v, want %s", got, err, tc.want)
			}
			if err != nil && !errors.Is(err, tc.cause) {
				t.Errorf("got %v, want it to report %v", err, tc.cause)
			}
			if refused != tc.refused {
				t.Errorf("got %d records refused, want %d", refused, tc.refused)
			}
		})
	}
}

// TestSendRetriesPendingRecords asserts that retrying a batch resends only
// the records of the partition that failed, in their original order, and
// leaves alone the records that the other partition already stored.
func TestSendRetriesPendingRecords(t *testing.T) {
	t.Parallel()

	var (
		c       = newFakeCluster(t, 2)
		events  = testEvents(8)
		failing atomic.Bool
	)
	failing.Store(true)
	failPartition(c, 0, kerr.NotEnoughReplicas, &failing)

	cfg := fakeConfig(c)
	cfg.SendTimeout = minSendTimeout

	var (
		s       = newTestSink(t, cfg)
		payload = encodeBatch(s, events...)
		id      = sink.NewBatchID()
	)
	if _, err := s.Send(t.Context(), payload, id); sink.Classify(err) != sink.ResultRetryable {
		t.Fatalf("got %v from first attempt, want a retryable error", err)
	}
	failing.Store(false)

	if _, err := s.Send(t.Context(), payload, id); err != nil {
		t.Fatalf("failed to retry batch: %v", err)
	}
	records := consumeRecords(t, c, len(events))
	if len(records) != len(events) {
		t.Fatalf("got %d records, want %d", len(records), len(events))
	}
	partitions := make(map[int32][]string)

	for _, r := range records {
		partitions[r.Partition] = append(partitions[r.Partition], string(r.Key))
	}
	if len(partitions) != 2 {
		t.Fatalf("got records on %d partitions, want 2", len(partitions))
	}
	for p, keys := range partitions {
		want := slices.DeleteFunc(eventKeys(events), func(key string) bool {
			return !slices.Contains(keys, key)
		})
		if !slices.Equal(keys, want) {
			t.Errorf("partition %d contains %v, want %v", p, keys, want)
		}
	}
}

// TestSendTimesOutOnSilentBroker asserts that a produce waiting for a broker
// response doesn't block past the send timeout, even when the response never
// comes, and that the batch is then reported as timed out.
func TestSendTimesOutOnSilentBroker(t *testing.T) {
	t.Parallel()

	c := newFakeCluster(t, 1)
	silenceProduce(c)

	cfg := fakeConfig(c)
	cfg.SendTimeout = minSendTimeout

	var (
		s       = newTestSink(t, cfg)
		timeout = time.Duration(cfg.SendTimeout)
	)
	ctx, cancel := context.WithTimeoutCause(t.Context(), timeout, sink.ErrSendTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := s.Send(ctx, encodeBatch(s, testEvent()), sink.NewBatchID())
		done <- err
	}()
	select {
	case err := <-done:
		if got := sink.Classify(err); got != sink.ResultTimeout {
			t.Errorf("got %s from %v, want %s", got, err, sink.ResultTimeout)
		}
	case <-time.After(timeout + time.Second):
		t.Fatalf("send didn't return within %v, want it to stop at the timeout", timeout+time.Second)
	}
}

// TestSendFollowsLeaderChange asserts that a record refused by a broker that
// no longer leads its partition reaches the new leader within the same attempt.
func TestSendFollowsLeaderChange(t *testing.T) {
	t.Parallel()

	var (
		c     = newFakeCluster(t, 1, kfake.NumBrokers(2))
		moved = moveLeaderOnProduce(c, 1)
		cfg   = fakeConfig(c)
		s     = newTestSink(t, cfg)
	)
	ctx, cancel := context.WithTimeoutCause(t.Context(), time.Duration(cfg.SendTimeout), sink.ErrSendTimeout)
	defer cancel()

	if _, err := s.Send(ctx, encodeBatch(s, testEvent()), sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send batch: %v", err)
	}
	if !moved.Load() {
		t.Fatal("no produce request reached the former leader")
	}
	if records := consumeRecords(t, c, 1); len(records) != 1 {
		t.Errorf("got %d records, want 1", len(records))
	}
}

// TestSendRefusesOversizedRecord asserts that a record over the maximum
// message size is counted as refused, while the rest of its batch is
// delivered.
func TestSendRefusesOversizedRecord(t *testing.T) {
	var (
		c      = newFakeCluster(t, 1)
		cfg    = fakeConfig(c)
		events = testEvents(3)
	)
	cfg.MaxMessageBytes = 4 << 10 // 4 KiB
	events[1].Note = strings.Repeat("x", 8<<10)

	s := newTestSink(t, cfg)

	refused, err := s.Send(t.Context(), encodeBatch(s, events...), sink.NewBatchID())
	if err != nil {
		t.Fatalf("failed to send batch: %v", err)
	}
	if refused != 1 {
		t.Errorf("got %d records refused, want 1", refused)
	}
	records := consumeRecords(t, c, 2)

	keys := make([]string, len(records))
	for i, r := range records {
		keys[i] = string(r.Key)
	}
	if want := []string{"uid-0", "uid-2"}; !slices.Equal(keys, want) {
		t.Errorf("got records %v, want %v", keys, want)
	}
}

// TestSendFailsOnUntrustedCertificate asserts that a broker certificate the
// client can't verify fails the batch permanently, with the verification error
// as its cause and before the attempt's deadline.
func TestSendFailsOnUntrustedCertificate(t *testing.T) {
	t.Parallel()

	c := newFakeCluster(t, 1, kfake.TLS(&tls.Config{
		Certificates: []tls.Certificate{testutil.SelfSigned(t)},
		MinVersion:   tls.VersionTLS12,
	}))
	cfg := fakeConfig(c)
	cfg.TLS.Insecure = false
	cfg.SendTimeout = minSendTimeout

	var (
		s       = newTestSink(t, cfg)
		timeout = time.Duration(cfg.SendTimeout)
	)
	ctx, cancel := context.WithTimeoutCause(t.Context(), timeout, sink.ErrSendTimeout)
	defer cancel()

	_, err := s.Send(ctx, encodeBatch(s, testEvent()), sink.NewBatchID())

	if got := sink.Classify(err); got != sink.ResultPermanent {
		t.Fatalf("got %s from %v, want %s", got, err, sink.ResultPermanent)
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); !ok {
		t.Errorf("got %v, want the certificate verification error as its cause", err)
	}
	if ctx.Err() != nil {
		t.Error("batch failed at the attempt's deadline, want it failed before")
	}
}

// TestSendFailsOnPlaintextListener asserts that a client configured to use TLS
// with a broker listening in plaintext fails a batch permanently. It expects
// the error that franz-go raises for a connection closed right after the dial
// as the cause of the returned error.
func TestSendFailsOnPlaintextListener(t *testing.T) {
	t.Parallel()

	cfg := testConfig()

	cfg.Brokers = []string{plaintextListener(t)}
	cfg.SendTimeout = minSendTimeout

	var (
		s       = newTestSink(t, cfg)
		timeout = time.Duration(cfg.SendTimeout)
	)
	ctx, cancel := context.WithTimeoutCause(t.Context(), timeout, sink.ErrSendTimeout)
	defer cancel()

	_, err := s.Send(ctx, encodeBatch(s, testEvent()), sink.NewBatchID())

	if got := sink.Classify(err); got != sink.ResultPermanent {
		t.Fatalf("got %s from %v, want %s", got, err, sink.ResultPermanent)
	}
	if _, ok := errors.AsType[*kgo.ErrFirstReadEOF](err); !ok {
		t.Errorf("got %v, want the closed connection as its cause", err)
	}
	if ctx.Err() != nil {
		t.Error("batch failed at the attempt's deadline, want it failed before")
	}
}

func TestSendBeforeOpen(t *testing.T) {
	s := New("events", testConfig(), slog.New(slog.DiscardHandler), testAgentMetadata())

	_, err := s.Send(t.Context(), nil, sink.NewBatchID())

	if !errors.Is(err, sink.ErrNotOpen) {
		t.Fatalf("got %v, want a sink that is not open", err)
	}
	if got := sink.Classify(err); got != sink.ResultPermanent {
		t.Errorf("got %s, want %s", got, sink.ResultPermanent)
	}
}

func TestSendAfterShutdown(t *testing.T) {
	s := newTestSink(t, fakeConfig(newFakeCluster(t, 1)))

	if err := s.Shutdown(t.Context()); err != nil {
		t.Fatalf("failed to shut down: %v", err)
	}
	_, err := s.Send(t.Context(), nil, sink.NewBatchID())

	if !errors.Is(err, sink.ErrClosed) {
		t.Fatalf("got %v, want a closed sink", err)
	}
	if got := sink.Classify(err); got != sink.ResultPermanent {
		t.Errorf("got %s, want %s", got, sink.ResultPermanent)
	}
}

// newTestSink returns an open sink, shut down when the test ends.
func newTestSink(t *testing.T, cfg Config) *Sink {
	t.Helper()

	s := New("events", cfg, slog.New(slog.DiscardHandler), testAgentMetadata())
	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to open sink: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	return s
}

// encodeBatch returns the payload that the drainer composes from events.
func encodeBatch(s *Sink, events ...*event.Event) []byte {
	var frames []byte

	for _, ev := range events {
		frames = s.Encoder().AppendEvent(frames, ev)
	}
	return s.Framer().Compose(nil, frames, len(events))
}

func equalHeaders(a, b kgo.RecordHeader) bool {
	return a.Key == b.Key && bytes.Equal(a.Value, b.Value)
}

func eventKeys(events []*event.Event) []string {
	keys := make([]string, len(events))

	for i, ev := range events {
		keys[i] = string(ev.UID)
	}
	return keys
}
