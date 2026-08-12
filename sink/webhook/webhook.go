package webhook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/compress"
)

const (
	maxDrainBytes = 4 << 20 // 4 MiB
)

var (
	_ sink.BatchSink = (*Sink)(nil)
	_ sink.Opener    = (*Sink)(nil)
)

// Sink delivers each batch as one HTTP request to a configured URL.
type Sink struct {
	name       string
	url        string
	method     string
	headers    http.Header
	tls        *tlsconfig.Config
	client     *http.Client
	closed     atomic.Bool
	encoder    sink.Encoder
	framer     sink.Framer
	mu         sync.Mutex
	compressor compress.Compressor
	signer     *signer
}

// New returns a sink delivering batches to an HTTP endpoint.
// The client is built by Open, so building the sink performs no I/O.
func New(name string, cfg Config, meta identity.AgentMetadata) (*Sink, error) {
	encoder, err := newEncoder(cfg, meta)
	if err != nil {
		return nil, err
	}
	compressor, err := compress.New(cfg.Compression)
	if err != nil {
		return nil, err
	}
	signer, err := newSigner(cfg.Signature)
	if err != nil {
		return nil, fmt.Errorf("signature: %w", err)
	}
	return &Sink{
		name:       name,
		url:        cfg.URL,
		method:     cfg.Method,
		headers:    newHeaders(cfg, meta),
		tls:        cfg.TLS,
		encoder:    encoder,
		framer:     newFramer(cfg),
		compressor: compressor,
		signer:     signer,
	}, nil
}

// Name implements the [sink.Sink] interface.
func (s *Sink) Name() string {
	return s.name
}

// Encoder implements the [sink.BatchSink] interface.
func (s *Sink) Encoder() sink.Encoder {
	return s.encoder
}

// Framer implements the [sink.BatchSink] interface.
func (s *Sink) Framer() sink.Framer {
	return s.framer
}

// Open implements the [sink.Opener] interface.
// It builds the client, reading whatever certificate the configuration
// points at. A later call does nothing, since a certificate authority
// change takes effect only after a restart.
func (s *Sink) Open(context.Context) error {
	if s.client != nil {
		return nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()

	if s.tls != nil {
		cfg, err := s.tls.Build()
		if err != nil {
			return fmt.Errorf("sink/webhook: %w", err)
		}
		transport.TLSClientConfig = cfg
	}
	s.client = &http.Client{
		Transport: transport,

		// Redirects are not followed. A 301, 302 or 303 turns the
		// request into a GET and drops the body, so the batch would
		// be reported as delivered having sent nothing. Both specs
		// recommend updating the URL instead.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return nil
}

// Send implements the [sink.BatchSink] interface.
// It delivers the composed payload as one request, compressed and signed
// when the configuration asks for it. The response covers the whole batch,
// so no record is ever refused.
// It returns [sink.ErrClosed] once Shutdown has been called.
func (s *Sink) Send(ctx context.Context, payload []byte, id sink.BatchID) (int, error) {
	switch {
	case s.closed.Load():
		return 0, fmt.Errorf("%w: %s", sink.ErrClosed, s.url)
	case s.client == nil:
		return 0, fmt.Errorf("%w: %s", sink.ErrNotOpen, s.url)
	}
	body := payload

	if s.compressor != nil {
		compressed, err := s.compress(payload)
		if err != nil {
			return 0, err
		}
		body = compressed
	}
	req, err := http.NewRequestWithContext(ctx, s.method, s.url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("%w: failed to build request: %w", sink.ErrPermanent, err)
	}
	req.Header = s.headers.Clone()

	if s.signer != nil {
		s.signer.sign(req.Header, id, body, time.Now())
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, classify(ctx, err)
	}
	defer func() {
		// The body is drained before it is closed, in order to return
		// the connection to the pool rather than dropping it. The transport
		// already drains one of up to 256 KiB itself, so this only changes
		// the outcome for larger bodies, up to maxDrainBytes.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
		_ = resp.Body.Close()
	}()
	return 0, classifyStatus(resp, time.Now())
}

// Shutdown implements the [sink.Sink] interface.
// It closes idle connections and releases the compressor, if any.
func (s *Sink) Shutdown(context.Context) error {
	s.closed.Store(true)

	if s.client != nil {
		s.client.CloseIdleConnections()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.compressor != nil {
		return s.compressor.Close()
	}
	return nil
}

// compress returns the compressed payload. A compressor reuses its
// writer and its buffer across calls, so the lock is what keeps a
// shutdown from releasing them while a send is using them.
func (s *Sink) compress(payload []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed.Load() {
		return nil, fmt.Errorf("%w: %s", sink.ErrClosed, s.url)
	}
	compressed, err := s.compressor.Compress(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to compress payload: %w", sink.ErrPermanent, err)
	}
	return compressed, nil
}
