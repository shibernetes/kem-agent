package graylog

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/netconn"
)

var (
	_ sink.BatchSink = (*Sink)(nil)
	_ sink.Opener    = (*Sink)(nil)
)

// Sink delivers each batch as GELF messages over one TCP connection,
// optionally secured with TLS. Every event is a null-terminated GELF
// frame, and a whole batch is written in a single call.
type Sink struct {
	name    string
	addr    string
	tls     tlsconfig.Config
	conn    *netconn.Conn
	encoder sink.Encoder
	framer  sink.Framer
}

// New returns a sink sending events to a Graylog GELF TCP input.
// The connection is built by Open and dialed on the first send,
// so building the sink performs no I/O.
func New(name string, cfg Config, id identity.AgentMetadata) *Sink {
	return &Sink{
		name:    name,
		addr:    net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		tls:     cfg.TLS,
		encoder: newEncoder(cfg, id),
		framer:  newFramer(),
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
// It builds the connection, reading whatever certificate the configuration
// points at, so a bad path leaves this sink unable to deliver until a later
// call succeeds. Calling it again once the connection is open does nothing,
// since a new certificate authority only takes effect after a restart.
func (s *Sink) Open(context.Context) error {
	if s.conn != nil {
		return nil
	}
	cfg, err := s.tls.Build()
	if err != nil {
		return fmt.Errorf("sink/graylog: %w", err)
	}
	s.conn = netconn.New(netconn.Config{
		Address: s.addr,
		TLS:     cfg,
	})
	return nil
}

// Send implements the [sink.BatchSink] interface.
// It writes the whole batch in one call over a connection reused across
// batches. A GELF TCP input answers nothing, so a completed write is a
// success and no record is ever refused.
func (s *Sink) Send(ctx context.Context, payload []byte, _ sink.BatchID) (int, error) {
	if s.conn == nil {
		return 0, fmt.Errorf("%w: %s", sink.ErrNotOpen, s.addr)
	}
	return 0, classify(ctx, s.conn.Send(ctx, payload))
}

// Shutdown implements the [sink.Sink] interface.
// It closes the connection to the GELF input.
func (s *Sink) Shutdown(context.Context) error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}
