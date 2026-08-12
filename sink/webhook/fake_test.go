package webhook

import (
	"context"
	"encoding/pem"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
)

// A recorder is an HTTPS server capturing the requests it receives.
type recorder struct {
	*httptest.Server

	mu          sync.Mutex
	received    []request
	respStatus  int
	respHeaders http.Header
	respBody    []byte
	connClosed  chan struct{}
}

type request struct {
	method  string
	headers http.Header
	body    []byte
}

// newRecorder returns a recorder serving until the test ends.
func newRecorder(t *testing.T) *recorder {
	t.Helper()

	rec := &recorder{
		respStatus:  http.StatusAccepted,
		respHeaders: http.Header{},
		connClosed:  make(chan struct{}, 1),
	}
	rec.Server = httptest.NewUnstartedServer(rec)
	rec.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state != http.StateClosed {
			return
		}
		select {
		case rec.connClosed <- struct{}{}:
		default:
		}
	}
	rec.StartTLS()
	t.Cleanup(rec.Close)

	return rec
}

// ServeHTTP implements the [http.Handler] interface.
func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	b, _ := io.ReadAll(req.Body)

	r.mu.Lock()
	r.received = append(r.received, request{
		method:  req.Method,
		headers: req.Header.Clone(),
		body:    b,
	})
	status, headers, body := r.respStatus, r.respHeaders.Clone(), r.respBody
	r.mu.Unlock()

	maps.Copy(w.Header(), headers)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// setReply sets the status and headers the recorder answers with.
func (r *recorder) setReply(status int, response http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.respStatus = status
	if response != nil {
		r.respHeaders = response
	}
}

// setBody sets the body the recorder answers with.
func (r *recorder) setBody(body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.respBody = body
}

// last returns the last request served by the server.
func (r *recorder) last(t *testing.T) request {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.received) == 0 {
		t.Fatal("no request received")
	}
	return r.received[len(r.received)-1]
}

// count returns how many requests the recorder received.
func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.received)
}

// closedConn reports whether one of the server's connections has already closed.
func (r *recorder) closedConn() bool {
	select {
	case <-r.connClosed:
		return true
	default:
		return false
	}
}

// awaitClosedConn reports whether one of the server's connections closes.
func (r *recorder) awaitClosedConn(d time.Duration) bool {
	select {
	case <-r.connClosed:
		return true
	case <-time.After(d):
		return false
	}
}

// caPEM returns the recorder's certificate authority.
func (r *recorder) caPEM(t *testing.T) string {
	t.Helper()

	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: r.Certificate().Raw,
	}))
}

// newTestSink returns a non-opened sink delivering to the destination
// the configuration names.
func newTestSink(t *testing.T, cfg Config) *Sink {
	t.Helper()

	s, err := New("hook", cfg, testAgentMetadata())
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	return s
}

// newTestOpenedSink returns an opened sink delivering to the destination
// the configuration names.
func newTestOpenedSink(t *testing.T, cfg Config) *Sink {
	t.Helper()

	s := newTestSink(t, cfg)
	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to open sink: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown(context.Background())
	})
	return s
}

// composeBatch runs the sink's encoder and framer over the
// given events, and returns the resulting payload.
func composeBatch(s *Sink, events ...*event.Event) []byte {
	var (
		framer = s.Framer()
		frames []byte
	)
	for i, ev := range events {
		if i > 0 {
			frames = append(frames, framer.Separator()...)
		}
		frames = s.Encoder().AppendEvent(frames, ev)
	}
	return framer.Compose(nil, frames, len(events))
}

func testConfig(t *testing.T, rec *recorder, opts func(*Config)) Config {
	t.Helper()

	cfg := DefaultConfig()

	cfg.Type = "webhook"
	cfg.URL = rec.URL
	cfg.TLS = &tlsconfig.Config{
		CAPEM: rec.caPEM(t),
	}
	if opts != nil {
		opts(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("configuration did not validate: %v", err)
	}
	return cfg
}

func testAgentMetadata() identity.AgentMetadata {
	return identity.AgentMetadata{
		Cluster:   "prod-eu-1",
		Node:      "node-001",
		Namespace: "kem-system",
		Pod:       "kem-agent-0",
		Version:   "0.0.1",
		Commit:    "abc1234",
	}
}

func testEvent() *event.Event {
	ev := &event.Event{
		UID:                 "d1f5c2a0-1111-2222-3333-444455556666",
		ResourceVersion:     "184213",
		Namespace:           "team-a",
		Name:                "web-1.17f0a2b3c4d5e6f7",
		EventTime:           metav1.NewMicroTime(time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)),
		ReportingController: "kubelet",
		ReportingInstance:   "kubelet-node-001",
		Action:              "Killing",
		Reason:              "OOMKilled",
		Note:                "Container web was OOM killed",
		Type:                "Warning",
		Regarding: corev1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Pod",
			Namespace:  "team-a",
			Name:       "web-1",
			UID:        "9c8b7a6d-5e4f-3210-9876-543210fedcba",
		},
	}
	ev.Labels = map[string]string{"app.kubernetes.io/name": "web", "tier": "front"}
	ev.Annotations = map[string]string{"team": "platform"}

	return ev
}
