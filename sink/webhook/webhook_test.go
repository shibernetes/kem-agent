package webhook

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/compress"
)

func TestName(t *testing.T) {
	if got := newTestSink(t, validConfig()).Name(); got != "hook" {
		t.Errorf("got name %q, want %q", got, "hook")
	}
}

// TestNewOpensNothing asserts that building the sink creates no client.
func TestNewOpensNothing(t *testing.T) {
	if s := newTestSink(t, validConfig()); s.client != nil {
		t.Error("the sink holds a client, want none until it is opened")
	}
}

func TestOpenBuildsOnce(t *testing.T) {
	s := newTestOpenedSink(t, testConfig(t, newRecorder(t), nil))

	first := s.client
	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to open sink: %v", err)
	}
	if s.client != first {
		t.Error("the client was rebuilt, want the first one kept")
	}
}

func TestSendBeforeOpen(t *testing.T) {
	_, err := newTestSink(t, validConfig()).Send(t.Context(), []byte("[]"), sink.NewBatchID())

	if !errors.Is(err, sink.ErrNotOpen) {
		t.Errorf("got %v, want the sink reported as not open", err)
	}
}

func TestSendDelivers(t *testing.T) {
	var (
		rec = newRecorder(t)
		s   = newTestOpenedSink(t, testConfig(t, rec, nil))
		b   = composeBatch(s, testEvent())
	)
	refused, err := s.Send(t.Context(), b, sink.NewBatchID())
	if err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if refused != 0 {
		t.Errorf("got %d refused records, want none", refused)
	}
	req := rec.last(t)
	if !bytes.Equal(req.body, b) {
		t.Errorf("got body %q, want %q", req.body, b)
	}
	if got := req.headers.Get(headerContentType); got != "application/json" {
		t.Errorf("got content-type header %q, want %q", got, "application/json")
	}
	if got := req.headers.Get(headerCluster); got != "prod-eu-1" {
		t.Errorf("got agent cluster header %q, want %q", got, "prod-eu-1")
	}
}

func TestSendUsesConfiguredMethod(t *testing.T) {
	rec := newRecorder(t)

	s := newTestOpenedSink(t, testConfig(t, rec, func(c *Config) {
		c.Method = http.MethodPut
	}))
	if _, err := s.Send(t.Context(), []byte("[]"), sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if got := rec.last(t).method; got != http.MethodPut {
		t.Errorf("got method %q, want %q", got, http.MethodPut)
	}
}

// TestSendCompresses asserts that a compressed body is sent with the
// appropriate content-encoding header naming the algorithm.
func TestSendCompresses(t *testing.T) {
	var (
		rec = newRecorder(t)
		s   = newTestOpenedSink(t, testConfig(t, rec, func(c *Config) {
			c.Compression = compress.Gzip
		}))
		b = composeBatch(s, testEvent())
	)
	if _, err := s.Send(t.Context(), b, sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	req := rec.last(t)

	if got := req.headers.Get(headerContentEncoding); got != compress.Gzip.String() {
		t.Errorf("got content-encoding header %q, want %q", got, compress.Gzip)
	}
	if bytes.Equal(req.body, b) {
		t.Error("the payload was sent as it was composed, want it compressed")
	}
	if got := gunzip(t, req.body); !bytes.Equal(got, b) {
		t.Errorf("got %q, want %q", got, b)
	}
}

func TestSendSigns(t *testing.T) {
	var (
		rec = newRecorder(t)
		id  = sink.NewBatchID()
		s   = newTestOpenedSink(t, testConfig(t, rec, func(c *Config) {
			c.Signature = testV1Signature()
		}))
	)
	if _, err := s.Send(t.Context(), composeBatch(s, testEvent()), id); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	req := rec.last(t)

	if got, want := req.headers.Get(headerID), messageIDPrefix+id.String(); got != want {
		t.Errorf("got id %q, want %q", got, want)
	}
	h := hmac.New(sha256.New, symmetricKey(32))
	h.Write(signedContent(req.headers, req.body))

	want := IdentifierV1 + "," + base64.StdEncoding.EncodeToString(h.Sum(nil))
	if got := req.headers.Get(headerSignature); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSendReportsStatus(t *testing.T) {
	cases := map[int]sink.Result{
		http.StatusAccepted:              sink.ResultSuccess,
		http.StatusBadRequest:            sink.ResultPermanent,
		http.StatusRequestEntityTooLarge: sink.ResultOversized,
		http.StatusServiceUnavailable:    sink.ResultRetryable,
	}
	for status, want := range cases {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			rec := newRecorder(t)
			rec.setReply(status, nil)

			s := newTestOpenedSink(t, testConfig(t, rec, nil))
			_, err := s.Send(t.Context(), []byte("[]"), sink.NewBatchID())

			if got := sink.Classify(err); got != want {
				t.Errorf("got result %s, want %s", got, want)
			}
		})
	}
}

// TestSendReportsRetryDelay asserts that a retry delay requested
// by the destination reaches the retry loop.
func TestSendReportsRetryDelay(t *testing.T) {
	rec := newRecorder(t)
	rec.setReply(http.StatusTooManyRequests, http.Header{"Retry-After": {"30"}})

	s := newTestOpenedSink(t, testConfig(t, rec, nil))
	_, err := s.Send(t.Context(), []byte("[]"), sink.NewBatchID())

	retry, ok := errors.AsType[*sink.RetryAfterError](err)
	if !ok {
		t.Fatalf("got type %T, want a *sink.RetryAfterError", err)
	}
	if retry.Delay != 30*time.Second {
		t.Errorf("got %s delay, want 30s", retry.Delay)
	}
}

// TestSendReportsDeadline asserts that a send attempt reaching its context
// deadline is reported as a timeout, rather than as a transport error that
// classified as retryable.
func TestSendReportsDeadline(t *testing.T) {
	s := newTestOpenedSink(t, testConfig(t, newRecorder(t), nil))

	_, err := s.Send(timedOutContext(t), []byte("[]"), sink.NewBatchID())
	if got := sink.Classify(err); got != sink.ResultTimeout {
		t.Errorf("got result %s, want %s", got, sink.ResultTimeout)
	}
}

// TestSendVerifiesCertificate asserts that the destination's certificate
// is verified by default.
func TestSendVerifiesCertificate(t *testing.T) {
	rec := newRecorder(t)
	s := newTestOpenedSink(t, testConfig(t, rec, func(c *Config) { c.TLS = &tlsconfig.Config{} }))

	if _, err := s.Send(t.Context(), []byte("[]"), sink.NewBatchID()); err == nil {
		t.Error("the batch was delivered, want the certificate refused")
	}
	if got := rec.count(); got != 0 {
		t.Errorf("the destination received %d requests, want none", got)
	}
}

// TestSendRefusesRedirect asserts that a redirect is answered rather
// than followed, since a 301, 302 or 303 turns the request into a GET
// and drops the payload.
func TestSendRefusesRedirect(t *testing.T) {
	rec := newRecorder(t)
	rec.setReply(http.StatusFound, http.Header{"Location": {rec.URL + "/moved"}})

	s := newTestOpenedSink(t, testConfig(t, rec, nil))
	_, err := s.Send(t.Context(), []byte("[]"), sink.NewBatchID())

	if got := sink.Classify(err); got != sink.ResultPermanent {
		t.Errorf("got result %s, want %s", got, sink.ResultPermanent)
	}
	if got := rec.count(); got != 1 {
		t.Errorf("the destination received %d requests, want the redirect refused", got)
	}
}

// TestSendDrainsResponse asserts that the response is read before it is
// closed, which returns the connection to the pool. The transport drains
// a response of up to 256 KiB itself, so only larger ones are concerned.
func TestSendDrainsResponse(t *testing.T) {
	rec := newRecorder(t)
	rec.setBody(bytes.Repeat([]byte("z"), 512<<10)) // 512 KiB

	s := newTestOpenedSink(t, testConfig(t, rec, nil))
	if _, err := s.Send(t.Context(), []byte("[]"), sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if !sendReusesConnection(t, s) {
		t.Error("the second request dialed again, want the first connection reused")
	}
}

// TestShutdownBeforeOpen asserts that a sink that never ran shuts down cleanly.
func TestShutdownBeforeOpen(t *testing.T) {
	for _, algorithm := range []compress.Algorithm{
		compress.None,
		compress.Gzip,
		compress.Zstd,
	} {
		t.Run(algorithm.String(), func(t *testing.T) {
			cfg := validConfig()
			cfg.Compression = algorithm

			if err := newTestSink(t, cfg).Shutdown(t.Context()); err != nil {
				t.Errorf("failed to shut down sink that was never opened: %v", err)
			}
		})
	}
}

// TestShutdownClosesIdleConnections asserts that the sink
// releases the client's connections it is holding on shutdown.
func TestShutdownClosesIdleConnections(t *testing.T) {
	var (
		r = newRecorder(t)
		s = newTestOpenedSink(t, testConfig(t, r, nil))
	)
	if _, err := s.Send(t.Context(), []byte("[]"), sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if r.closedConn() {
		t.Fatal("connection closed before shutdown, want none")
	}
	if err := s.Shutdown(t.Context()); err != nil {
		t.Fatalf("failed to shut down: %v", err)
	}
	if !r.awaitClosedConn(time.Second) {
		t.Error("no connection closed, want the idle one closed")
	}
}

// TestSendAfterShutdown asserts that a sink that was shut down
// delivers nothing, rather than dialing the destination again.
func TestSendAfterShutdown(t *testing.T) {
	var (
		r = newRecorder(t)
		s = newTestOpenedSink(t, testConfig(t, r, nil))
	)
	if err := s.Shutdown(t.Context()); err != nil {
		t.Fatalf("failed to shut down: %v", err)
	}
	_, err := s.Send(t.Context(), []byte("[]"), sink.NewBatchID())
	if !errors.Is(err, sink.ErrClosed) {
		t.Errorf("got %v, want the sink reported as closed", err)
	}
	if got := r.count(); got != 0 {
		t.Errorf("the destination received %d requests, want none", got)
	}
}

// sendReusesConnection sends one payload and reports whether it
// was sent using a reused connection.
func sendReusesConnection(t *testing.T, s *Sink) bool {
	t.Helper()

	var reused bool
	ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			reused = info.Reused
		},
	})
	if _, err := s.Send(ctx, []byte("[]"), sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	return reused
}

// timedOutContext returns a context whose deadline has passed.
func timedOutContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeoutCause(t.Context(), time.Millisecond, sink.ErrSendTimeout)
	t.Cleanup(cancel)
	<-ctx.Done()

	return ctx
}

func gunzip(t *testing.T, data []byte) []byte {
	t.Helper()

	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("data is not gzipped: %v", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	return b
}

func TestShutdownDuringSend(t *testing.T) {
	cfg := validConfig()
	cfg.Compression = compress.Gzip

	s := newTestOpenedSink(t, cfg)
	payload := []byte("foobar")

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
