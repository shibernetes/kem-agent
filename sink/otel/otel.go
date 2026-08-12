package otel

import (
	"context"
	"fmt"
	"sync/atomic"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"

	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	// exportMethod is the full method of the OTLP logs service. The
	// generated client cannot be used, since it marshals a request
	// of its own where this sink sends bytes it composed itself.
	exportMethod = "/opentelemetry.proto.collector.logs.v1.LogsService/Export"
)

var (
	_ sink.BatchSink = (*Sink)(nil)
	_ sink.Opener    = (*Sink)(nil)
)

// Sink delivers each batch as one OTLP export request over gRPC.
type Sink struct {
	name     string
	target   string
	tls      *tlsconfig.Config
	metadata metadata.MD
	opts     []grpc.CallOption
	conn     *grpc.ClientConn
	encoder  sink.Encoder
	framer   sink.Framer
	closed   atomic.Bool
}

// New returns a sink exporting batches to an OTLP endpoint.
// The client is built by Open, so creating the sink performs no I/O.
func New(name string, cfg Config, meta identity.AgentMetadata) *Sink {
	opts := []grpc.CallOption{grpc.ForceCodecV2(rawCodec{})}

	if cfg.Compression == CompressionGzip {
		opts = append(opts, grpc.UseCompressor(gzip.Name))
	}
	return &Sink{
		name:     name,
		target:   cfg.grpcDialTarget(),
		tls:      transportSecurity(cfg),
		metadata: exportMetadata(cfg),
		opts:     opts,
		encoder:  newEncoder(meta),
		framer:   framer{},
	}
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
// points at. A later call does nothing, since a new certificate authority
// only takes effect after a restart. The connection itself is dialed on
// the first export.
func (s *Sink) Open(context.Context) error {
	if s.conn != nil {
		return nil
	}
	creds := insecure.NewCredentials()

	if s.tls != nil {
		cfg, err := s.tls.Build()
		if err != nil {
			return fmt.Errorf("sink/otel: %w", err)
		}
		creds = credentials.NewTLS(cfg)
	}
	conn, err := grpc.NewClient(s.target,
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(s.opts...),
	)
	if err != nil {
		return fmt.Errorf("sink/otel: failed to create client: %w", err)
	}
	s.conn = conn

	return nil
}

// Send implements the [sink.BatchSink] interface.
// It exports the composed payload as one request, and reports
// how many records the target refused after accepting it.
func (s *Sink) Send(ctx context.Context, payload []byte, _ sink.BatchID) (int, error) {
	switch {
	case s.closed.Load():
		return 0, fmt.Errorf("%w: %s", sink.ErrClosed, s.target)
	case s.conn == nil:
		return 0, fmt.Errorf("%w: %s", sink.ErrNotOpen, s.target)
	}
	if len(s.metadata) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, s.metadata)
	}
	var resp collogs.ExportLogsServiceResponse

	if err := s.conn.Invoke(ctx, exportMethod, payload, &resp); err != nil {
		return 0, classify(ctx, err)
	}
	return refusedRecords(&resp), nil
}

// Shutdown implements the [sink.Sink] interface.
// It closes the connection to the target. The sink delivers
// nothing afterwards.
func (s *Sink) Shutdown(context.Context) error {
	s.closed.Store(true)

	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

// transportSecurity returns the TLS settings the connection is
// dialed with, or nil for a plaintext one. An HTTPS endpoint without
// a TLS block is dialed with the defaults of an empty TLS config.
func transportSecurity(cfg Config) *tlsconfig.Config {
	if !cfg.isSecure() {
		return nil
	}
	if cfg.TLS != nil {
		return cfg.TLS
	}
	return &tlsconfig.Config{}
}

// exportMetadata returns the configured headers as gRPC metadata,
// which is sent with every export request.
func exportMetadata(cfg Config) metadata.MD {
	if len(cfg.Headers) == 0 {
		return nil
	}
	md := make(metadata.MD, len(cfg.Headers))
	for name, value := range cfg.Headers {
		md.Set(name, string(value))
	}
	return md
}
