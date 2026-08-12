package netconn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateAddress(t *testing.T) {
	cases := map[string]struct {
		host  string
		port  int
		valid bool
	}{
		"hostname":           {"example.com", 443, true},
		"qualified hostname": {"graylog.svc.cluster.local", 12201, true},
		"IPv4":               {"10.0.0.1", 9200, true},
		"IPv6":               {"2001:db8::1", 443, true},
		"IPv6 with a zone":   {"fe80::1%eth0", 443, true},
		"min port":           {"host", 1, true},
		"max port":           {"host", 65535, true},
		"no host":            {"", 80, false},
		"host with scheme":   {"http://host", 80, false},
		"host with space":    {"ho st", 80, false},
		"host with port":     {"host:80", 80, false},
		"bracketed IPv6":     {"[2001:db8::1]", 443, false},
		"zero port":          {"host", 0, false},
		"invalid port":       {"host", 65536, false},
		"negative port":      {"host", -1, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateAddress(tc.host, tc.port)
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the address accepted", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the address rejected")
			}
		})
	}
}

func TestSendDelivers(t *testing.T) {
	read := make(chan string, 1)
	c := newConn(t, listen(t, readInto(read, nil)))

	send(t, c, "foobar")
	if got := receive(t, read); got != "foobar" {
		t.Errorf("read %q, want %q", got, "foobar")
	}
}

func TestSendReusesConn(t *testing.T) {
	var (
		dials atomic.Int64
		r     = make(chan string, 2)
		c     = newConn(t, listen(t, readInto(r, &dials)))
	)
	send(t, c, "first")
	receive(t, r)
	send(t, c, "second")
	receive(t, r)

	if got := dials.Load(); got != 1 {
		t.Errorf("got %d connections, want the first one reused", got)
	}
}

// TestSendRedialsReusedConn asserts that a write failing on a connection
// reused from an earlier send is retried with a new one. A server closing
// an idle connection is the usual cause.
func TestSendRedialsReusedConn(t *testing.T) {
	var (
		dials atomic.Int64
		r     = make(chan string, 2)
		c     = newConn(t, listen(t, readInto(r, &dials)))
	)
	send(t, c, "first")
	receive(t, r)

	if err := c.conn.Close(); err != nil {
		t.Fatalf("failed to close conn: %v", err)
	}
	send(t, c, "second")

	if got := receive(t, r); got != "second" {
		t.Errorf("read %q, want %q", got, "second")
	}
	if got := dials.Load(); got != 2 {
		t.Errorf("got %d connections, want a second one dialed", got)
	}
}

func TestSendClearsPreviousDeadline(t *testing.T) {
	var (
		dials atomic.Int64
		r     = make(chan string, 1)
		c     = newConn(t, listen(t, readInto(r, &dials)))
	)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	if err := c.Send(ctx, []byte("first")); err != nil {
		t.Fatalf("failed to send: %v", err)
	}
	receive(t, r)
	time.Sleep(100 * time.Millisecond)

	// The next send context has no deadline, and the one above has passed.
	send(t, c, "second")

	if got := receive(t, r); got != "second" {
		t.Errorf("read %q, want %q", got, "second")
	}
	// A stale deadline fails the write, and the retry on a reused
	// connection then dials a fresh one and succeeds, so the payload
	// still arrives and only the connection count reports it.
	if got := dials.Load(); got != 1 {
		t.Errorf("got %d connections, want the deadline cleared rather than redialed", got)
	}
}

// TestConnWithTLS asserts that the configured TLS settings are used
// to establish a connection.
func TestConnWithTLS(t *testing.T) {
	var (
		s = &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}
		r = make(chan string, 1)
		c = New(Config{
			Address: listenTLS(t, s, readInto(r, nil)),
			TLS:     &tls.Config{InsecureSkipVerify: true},
		})
	)
	t.Cleanup(func() {
		_ = c.Close()
	})
	send(t, c, "foobar")

	if got := receive(t, r); got != "foobar" {
		t.Errorf("read %q, want %q", got, "foobar")
	}
}

func TestSendToClosedPort(t *testing.T) {
	c := newConn(t, closedAddr(t))

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if err := c.Send(ctx, []byte("x")); err == nil {
		t.Error("send succeeded on a closed port, want it to fail")
	}
}

// TestSendUnblocksOnCancel asserts that a write wedged against a receiver
// that stopped reading returns when the context is canceled, which a write
// deadline alone cannot do.
func TestSendUnblocksOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	// The conn is never read, so the write blocks once the buffers fill.
	// Closing the conn at the end keeps it reachable until then, otherwise
	// it could be GC'ed by the finalizer set by the net package.
	conn := newConn(t, listen(t, func(conn net.Conn) {
		<-t.Context().Done()
		_ = conn.Close()
	}))
	r := make(chan error, 1)

	go func() {
		r <- conn.Send(ctx, make([]byte, 8<<20)) // 8 MiB
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-r:
		if err == nil {
			t.Error("the send completed, want the cancellation to interrupt it")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the send never returned, want it unblocked")
	}
}

func TestSendAfterClose(t *testing.T) {
	c := newConn(t, closedAddr(t))

	if err := c.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}
	if err := c.Send(t.Context(), []byte("x")); !errors.Is(err, ErrClosed) {
		t.Errorf("got %v, want the connection reported closed", err)
	}
}

func TestCloseBeforeDial(t *testing.T) {
	if err := New(Config{Address: closedAddr(t)}).Close(); err != nil {
		t.Errorf("failed to close a connection that never opened: %v", err)
	}
}

// TestCloseReleasesConn asserts that Close closes the socket rather than
// only marking the connection unusable, which the server sees as the end
// of the stream.
func TestCloseReleasesConn(t *testing.T) {
	done := make(chan struct{}, 1)

	conn := newConn(t, listen(t, func(conn net.Conn) {
		buf := make([]byte, 64)
		for {
			if _, err := conn.Read(buf); err != nil {
				done <- struct{}{}
				return
			}
		}
	}))
	// The connection is dialed on the first send, so it
	// needs to send a payload at least once to be opened.
	send(t, conn, "hello world!")

	if err := conn.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("connection is still open, want it closed")
	}
}

func TestCloseIdempotency(t *testing.T) {
	var (
		r = make(chan string, 1)
		c = newConn(t, listen(t, readInto(r, nil)))
	)
	send(t, c, "xyz")
	receive(t, r)

	for range 3 {
		if err := c.Close(); err != nil {
			t.Fatalf("failed to close: %v", err)
		}
	}
}

func TestCloseDuringSend(t *testing.T) {
	addr := listen(t, func(conn net.Conn) {
		defer func() {
			_ = conn.Close()
		}()
		buf := make([]byte, 64)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	})
	for range 200 {
		c := New(Config{Address: addr})

		var wg sync.WaitGroup
		wg.Go(func() {
			for range 20 {
				err := c.Send(t.Context(), []byte("x"))
				if err != nil {
					return
				}
			}
		})
		wg.Go(func() {
			_ = c.Close()
		})
		wg.Wait()
	}
}

func newConn(t *testing.T, addr string) *Conn {
	t.Helper()

	c := New(Config{Address: addr})
	t.Cleanup(func() {
		_ = c.Close()
	})
	return c
}

func send(t *testing.T, c *Conn, payload string) {
	t.Helper()

	if err := c.Send(t.Context(), []byte(payload)); err != nil {
		t.Fatalf("failed to send %q: %v", payload, err)
	}
}

func receive(t *testing.T, read <-chan string) string {
	t.Helper()

	select {
	case got := <-read:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("read nothing, want the payload")
	}
	return ""
}

// listen starts a loopback listener serving every connection
// with handle, and returns the address to dial.
func listen(t *testing.T, handle func(net.Conn)) string {
	t.Helper()

	return serve(t, newListener(t), handle)
}

// listenTLS starts a loopback listener secured with cfg.
func listenTLS(t *testing.T, cfg *tls.Config, handle func(net.Conn)) string {
	t.Helper()

	return serve(t, tls.NewListener(newListener(t), cfg), handle)
}

func newListener(t *testing.T) net.Listener {
	t.Helper()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	return ln
}

// serve accepts connections until the listener closes.
func serve(t *testing.T, ln net.Listener, handle func(net.Conn)) string {
	t.Helper()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(conn)
		}
	}()
	return ln.Addr().String()
}

// readInto returns a handler forwarding everything it reads, and
// counting the connections it served when a counter is given.
func readInto(read chan<- string, dialed *atomic.Int64) func(net.Conn) {
	return func(conn net.Conn) {
		defer func() {
			_ = conn.Close()
		}()
		if dialed != nil {
			dialed.Add(1)
		}
		buf := make([]byte, 64)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			read <- string(buf[:n])
		}
	}
}

// closedAddr returns a loopback address whose port
// has been released, so a dial is refused.
func closedAddr(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr := ln.Addr().String()

	if err := ln.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}
	return addr
}

// selfSigned returns a certificate for a test listener to serve.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "graylog"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
