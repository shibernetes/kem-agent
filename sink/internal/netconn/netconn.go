package netconn

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/shibernetes/kem-agent/config/diag"
)

// ErrClosed is returned by Send when a connection has been closed.
var ErrClosed = errors.New("connection is closed")

// Conn is a lazily-dialed TCP connection to one destination, optionally
// secured with TLS, and reused across sends. It opens the connection on
// the first [Conn.Send] and keeps it open, applying TCP keep-alive and
// redialing automatically when a write fails on a connection carried over
// from a previous send.
type Conn struct {
	conn   net.Conn
	addr   string
	tls    *tls.Config
	closed bool
	mu     sync.Mutex
}

// Config configures a [Conn].
// A nil TLS configuration dials the destination in plaintext.
type Config struct {
	Address string
	TLS     *tls.Config
}

// contextDialer is the shared shape of [net.Dialer] and [tls.Dialer], so
// dial opens either kind under the context deadline through one call.
type contextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// New builds a [Conn] from the configuration.
// It performs no I/O since the connection is opened on the first [Conn.Send].
func New(cfg Config) *Conn {
	return &Conn{
		addr: cfg.Address,
		tls:  cfg.TLS,
	}
}

// Close closes the connection.
// It is a no-op if the connection is already closed.
func (c *Conn) Close() error {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.closed = true
	c.mu.Unlock()

	if conn == nil {
		return nil
	}
	return conn.Close()
}

// Send writes b to the destination in a single write bounded by ctx, dialing
// lazily and reusing the connection across calls. A write failure on a
// connection reused from a previous send usually means the server closed it
// while idle, so Send redials and retries once, unless ctx is already done.
// A freshly dialed connection's failure is returned as-is, and the returned
// error is the raw transport error.
// Send is not safe for concurrent use, but Close may be called simultaneously.
func (c *Conn) Send(ctx context.Context, b []byte) error {
	reused, err := c.write(ctx, b)
	if err != nil && reused && ctx.Err() == nil {
		_, err = c.write(ctx, b)
	}
	return err
}

// dial opens the connection and reports whether it was already open.
// The lock is held during the dial so that a concurrent Close waits
// for it rather than ignoring it.
func (c *Conn) dial(ctx context.Context) (net.Conn, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case c.closed:
		return nil, false, ErrClosed
	case c.conn != nil:
		return c.conn, true, nil
	}
	netDialer := &net.Dialer{
		KeepAliveConfig: net.KeepAliveConfig{Enable: true},
	}
	var dialer contextDialer = netDialer

	if c.tls != nil {
		dialer = &tls.Dialer{NetDialer: netDialer, Config: c.tls}
	}
	conn, err := dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, false, err
	}
	c.conn = conn

	return conn, false, nil
}

// discard closes conn and forgets it, so that the next send dials again.
func (c *Conn) discard(conn net.Conn) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
	_ = conn.Close()
}

// write writes b over the open connection, dialing one when there is none,
// and reports whether that connection was carried over from an earlier send.
// A failed write discards the connection, so the next one dials again.
func (c *Conn) write(ctx context.Context, b []byte) (bool, error) {
	conn, reused, err := c.dial(ctx)
	if err != nil {
		return false, err
	}
	// A context with no deadline yields the zero time, which is exactly
	// what clears one, so the second return parameter is discarded and
	// the deadline set unconditionally. Setting it only when there one
	// is returned would leave a previous deadline in place, and fails
	// every write after it.
	deadline, _ := ctx.Deadline()
	err = conn.SetWriteDeadline(deadline)

	if err == nil {
		// A receiver that stops reading blocks the write once the socket
		// buffers fill, and the deadline cannot help when the context is
		// canceled ahead of it. Closing the connection during the write
		// unblocks it.
		stop := context.AfterFunc(ctx, func() {
			_ = conn.Close()
		})
		_, err = conn.Write(b)
		stop()
	}
	if err != nil {
		c.discard(conn)
		return reused, err
	}
	return reused, nil
}

// ValidateAddress checks that host is a bare hostname or IP address,
// and port a usable TCP port number.
func ValidateAddress(host string, port int) error {
	if host == "" {
		return diag.Pathf("host", "host is required")
	}
	if _, err := netip.ParseAddr(host); err != nil && strings.ContainsAny(host, "/ :") {
		return diag.Pathf("host", "invalid host %q, must be a bare hostname or IP address", host)
	}
	if port < 1 || port > 65535 {
		return diag.Pathf("port", "invalid port %d", port)
	}
	return nil
}
