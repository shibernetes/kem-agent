package otel

import (
	"context"
	"errors"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/sink"
)

func TestOpenKeepsClient(t *testing.T) {
	s := newTestOpenedSink(t, testConfig(t, newCollector(t), nil))
	conn := s.conn

	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to open sink: %v", err)
	}
	if s.conn != conn {
		t.Error("the client was replaced, want the one already open")
	}
}

func TestSendExportsBatch(t *testing.T) {
	col := newCollector(t)
	s := newTestOpenedSink(t, testConfig(t, col, nil))

	refused, err := s.Send(t.Context(), composeBatch(s, testEvent(), fullEvent()), sink.NewBatchID())
	if err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if refused != 0 {
		t.Errorf("got %d refused records, want none", refused)
	}
	if got := len(col.lastRequest(t).GetResourceLogs()); got != 2 {
		t.Errorf("got %d entries, want 2", got)
	}
	if got := col.requestCount(); got != 1 {
		t.Errorf("got %d requests, want 1", got)
	}
}

func TestSendCompresses(t *testing.T) {
	col := newCollector(t)

	s := newTestOpenedSink(t, testConfig(t, col, func(c *Config) {
		c.Compression = CompressionGzip
	}))
	if _, err := s.Send(t.Context(), composeBatch(s, fullEvent()), sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if got := col.recordCount(); got != 1 {
		t.Errorf("got %d records, want 1", got)
	}
	sizes := col.lastRequestPayloadSizes()

	if sizes.compressed >= sizes.raw {
		t.Errorf("got %d compressed bytes for %d of payload, want fewer",
			sizes.compressed, sizes.raw)
	}
}

// TestSendWithoutCompression asserts that the request is sent as it was
// composed when compression is disabled.
func TestSendWithoutCompression(t *testing.T) {
	col := newCollector(t)

	s := newTestOpenedSink(t, testConfig(t, col, func(c *Config) {
		c.Compression = CompressionNone
	}))
	payload := composeBatch(s, fullEvent())

	if _, err := s.Send(t.Context(), payload, sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if got := col.lastRequestPayloadSizes().compressed; got != len(payload) {
		t.Errorf("got %d compressed bytes, want %d", got, len(payload))
	}
}

// TestSendOverTLS asserts that a sink dials the target with the
// certificate authority it was configured with when TLS is enabled.
func TestSendOverTLS(t *testing.T) {
	col := newSecureCollector(t)
	s := newTestOpenedSink(t, testConfig(t, col, nil))

	if _, err := s.Send(t.Context(), composeBatch(s, testEvent()), sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if got := col.requestCount(); got != 1 {
		t.Errorf("got %d requests, want 1", got)
	}
}

// TestSendSendsHeaders asserts that the configured headers reach the
// target as request metadata.
func TestSendSendsHeaders(t *testing.T) {
	col := newSecureCollector(t)

	s := newTestOpenedSink(t, testConfig(t, col, func(c *Config) {
		c.Headers = map[string]opaque.String{"x-scope-orgid": "team-a"}
	}))
	if _, err := s.Send(t.Context(), composeBatch(s, testEvent()), sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	got := col.header("x-scope-orgid")

	if len(got) != 1 || got[0] != "team-a" {
		t.Errorf("got %v, want the configured header", got)
	}
}

// TestSendReportsRefusedRecords asserts that a partial success is
// counted rather than retried.
func TestSendReportsRefusedRecords(t *testing.T) {
	col := newCollector(t)
	col.setResp(partialSuccess(67), nil)

	s := newTestOpenedSink(t, testConfig(t, col, nil))

	refused, err := s.Send(t.Context(), composeBatch(s, testEvent()), sink.NewBatchID())
	if err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if refused != 67 {
		t.Errorf("got %d refused records, want 67", refused)
	}
}

func TestSendClassifiesFailures(t *testing.T) {
	cases := map[string]struct {
		err  error
		want sink.Result
	}{
		"unavailable":      {status.Error(codes.Unavailable, "down"), sink.ResultRetryable},
		"invalid argument": {status.Error(codes.InvalidArgument, "malformed"), sink.ResultPermanent},
		"exhausted":        {status.Error(codes.ResourceExhausted, "too large"), sink.ResultOversized},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			col := newCollector(t)
			col.setResp(nil, tc.err)
			s := newTestOpenedSink(t, testConfig(t, col, nil))

			_, err := s.Send(t.Context(), composeBatch(s, testEvent()), sink.NewBatchID())
			if got := sink.Classify(err); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// TestSendBeforeOpen asserts that a non-opened sink refuses the batch
// rather than retrying it, since nothing in the retry loop opens a sink.
func TestSendBeforeOpen(t *testing.T) {
	_, err := newTestSink(t, validConfig()).Send(t.Context(), nil, sink.NewBatchID())

	if !errors.Is(err, sink.ErrNotOpen) {
		t.Fatalf("got %v, want a sink that is not open", err)
	}
	if got := sink.Classify(err); got != sink.ResultPermanent {
		t.Errorf("got %s, want %s", got, sink.ResultPermanent)
	}
}

func TestSendAfterShutdown(t *testing.T) {
	s := newTestOpenedSink(t, testConfig(t, newCollector(t), nil))

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

func TestShutdownWithoutOpen(t *testing.T) {
	if err := newTestSink(t, validConfig()).Shutdown(t.Context()); err != nil {
		t.Errorf("failed to shut down: %v", err)
	}
}

func TestShutdownDuringSend(t *testing.T) {
	col := newCollector(t)
	s := newTestSink(t, testConfig(t, col, nil))

	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to open sink: %v", err)
	}
	payload := composeBatch(s, testEvent())

	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 50 {
			_, _ = s.Send(context.Background(), payload, sink.NewBatchID())
		}
	}()
	go func() {
		defer wg.Done()
		_ = s.Shutdown(context.Background())
	}()
	wg.Wait()
}
