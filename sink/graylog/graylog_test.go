package graylog

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/netconn"
)

func TestName(t *testing.T) {
	if got := newSink(t, testConfig()).Name(); got != "graylog" {
		t.Errorf("got name %q, want %q", got, "graylog")
	}
}

// TestNewDialsNothing asserts that building the sink opens no connection.
func TestNewDialsNothing(t *testing.T) {
	if s := newSink(t, testConfig()); s.conn != nil {
		t.Error("the sink holds a connection, want none until it is opened")
	}
}

func TestEncoderAndFramer(t *testing.T) {
	cfg := testConfig()
	cfg.SourceHost = "graylog-forwarder"

	s := newSink(t, cfg)
	frame := s.Encoder().AppendEvent(nil, &event.Event{
		Reason: "OOMKilled",
	})
	if len(frame) == 0 || frame[len(frame)-1] != 0 {
		t.Fatalf("got %q, want a null-terminated GELF message", frame)
	}
	assertContains(t, string(frame[:len(frame)-1]), `"host":"graylog-forwarder"`)

	if got := s.Framer().Fixed(); got != 0 {
		t.Errorf("got %d fixed bytes, want 0", got)
	}
}

// TestOpenBuildsOnce asserts that subsequent calls to Open keep using
// the existing connection. A change of certificate authority takes
// effect only after a restart.
func TestOpenBuildsOnce(t *testing.T) {
	s := newSink(t, testConfig())
	openSink(t, s)

	first := s.conn
	openSink(t, s)

	if s.conn != first {
		t.Error("the connection was recreated, want the first one kept")
	}
}

func TestOpenReadsCertificates(t *testing.T) {
	cfg := testConfig()
	cfg.TLS.CAFile = filepath.Join(t.TempDir(), "missing.pem")

	if err := newSink(t, cfg).Open(t.Context()); err == nil {
		t.Error("the sink opened with an unreadable certificate authority file, want it refused")
	}
}

func TestSendBeforeOpen(t *testing.T) {
	_, err := newSink(t, testConfig()).Send(t.Context(), []byte("frame\x00"), sink.NewBatchID())

	if !errors.Is(err, sink.ErrNotOpen) {
		t.Errorf("got %v, want the sink reported as not open", err)
	}
}

func TestSendDelivers(t *testing.T) {
	const frame = "frame\x00"

	cfg, in := listen(t)
	s := newSink(t, cfg)
	openSink(t, s)

	refused, err := s.Send(t.Context(), []byte(frame), sink.NewBatchID())
	if err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if refused != 0 {
		t.Errorf("got %d refused records, want none", refused)
	}
	select {
	case got := <-in:
		if string(got) != frame {
			t.Errorf("the input read %q, want %q", got, frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the input read nothing, want the batch")
	}
}

// TestSendReportsDeadline asserts that a send attempt reaching the
// context deadline is reported as a timeout, rather than as a transport
// error that can be retried.
func TestSendReportsDeadline(t *testing.T) {
	s := newSink(t, testConfig())
	openSink(t, s)

	_, err := s.Send(timedOutContext(t), []byte("frame\x00"), sink.NewBatchID())
	if got := sink.Classify(err); got != sink.ResultTimeout {
		t.Errorf("got result %q, want %q", got, sink.ResultTimeout)
	}
}

func TestShutdownBeforeOpen(t *testing.T) {
	if err := newSink(t, testConfig()).Shutdown(t.Context()); err != nil {
		t.Errorf("failed to shut down a sink that never opened: %v", err)
	}
}

// TestShutdownClosesConnection asserts that the sink releases its
// connection, so that a call to Send after it opens nothing.
func TestShutdownClosesConnection(t *testing.T) {
	const frame = "frame\x00"

	cfg, _ := listen(t)
	s := newSink(t, cfg)
	openSink(t, s)

	if _, err := s.Send(t.Context(), []byte(frame), sink.NewBatchID()); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	if err := s.Shutdown(t.Context()); err != nil {
		t.Fatalf("failed to shut down: %v", err)
	}
	if _, err := s.Send(t.Context(), []byte(frame), sink.NewBatchID()); !errors.Is(err, sink.ErrClosed) {
		t.Errorf("got %v, want the sink reported as closed", err)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		ctx  func(*testing.T) context.Context
		err  error
		want sink.Result
	}{
		"completed write": {
			want: sink.ResultSuccess,
		},
		"broken connection": {
			err:  errors.New("connection reset by peer"),
			want: sink.ResultRetryable,
		},
		"unverified certificate": {
			err:  &tls.CertificateVerificationError{Err: errors.New("unknown authority")},
			want: sink.ResultPermanent,
		},
		"deadline exceeded": {
			ctx:  timedOutContext,
			err:  errors.New("i/o timeout"),
			want: sink.ResultTimeout,
		},
		"released connection": {
			err:  netconn.ErrClosed,
			want: sink.ResultPermanent,
		},
		"graceful shutdown": {
			ctx:  canceledContext,
			err:  errors.New("use of closed network connection"),
			want: sink.ResultRetryable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(t)
			}
			if got := sink.Classify(classify(ctx, tc.err)); got != tc.want {
				t.Errorf("got result %q, want %q", got, tc.want)
			}
		})
	}
}

func newSink(t *testing.T, cfg Config) *Sink {
	t.Helper()

	return New("graylog", cfg, agentMetadata())
}

func openSink(t *testing.T, s *Sink) {
	t.Helper()

	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to open sink: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown(context.Background())
	})
}

// listen starts a loopback listener and returns a sink config addressing it.
// It sends the messages received to the returned channel.
func listen(t *testing.T) (Config, <-chan []byte) {
	t.Helper()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("got a %T, want a TCP address", ln.Addr())
	}
	cfg := testConfig()
	cfg.Host = addr.IP.String()
	cfg.Port = addr.Port
	cfg.TLS.Insecure = true

	ch := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() {
			_ = conn.Close()
		}()
		buf := make([]byte, 64)

		if n, err := conn.Read(buf); err == nil {
			ch <- buf[:n]
		}
	}()
	return cfg, ch
}

// timedOutContext returns a context whose deadline has passed.
func timedOutContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeoutCause(t.Context(), time.Millisecond, sink.ErrSendTimeout)
	t.Cleanup(cancel)
	<-ctx.Done()

	return ctx
}

// canceledContext returns a context canceled with a cause.
func canceledContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("shutting down"))

	return ctx
}
