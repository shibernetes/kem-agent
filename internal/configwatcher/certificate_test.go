package configwatcher

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewCertificate(t *testing.T) {
	cert, key := writeKeypair(t, t.TempDir())
	c := mustLoad(t, cert, key)

	got, err := c.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("failed to get certificate: %v", err)
	}
	if got == nil {
		t.Error("got no certificate, want the cached one")
	}
}

func TestNewCertificateErrors(t *testing.T) {
	var (
		dir          = t.TempDir()
		cert, key    = writeKeypair(t, dir)
		absent       = filepath.Join(dir, "absent.pem")
		otherCert, _ = newKeyPair(t)
		mismatched   = writeFile(t, dir, "other.crt", otherCert)
	)
	cases := map[string][2]string{
		"missing certificate": {absent, key},
		"missing key":         {cert, absent},
		"mismatched pair":     {mismatched, key},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewCertificate(files[0], files[1]); err == nil {
				t.Error("got no error, want the load to fail")
			}
		})
	}
}

func TestReloadKeepsLastGoodPair(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeKeypair(t, dir)
	c := mustLoad(t, cert, key)

	tlsCert, _ := c.GetClientCertificate(nil)

	otherCert, _ := newKeyPair(t)
	writeFile(t, dir, filepath.Base(cert), otherCert)
	c.reload(t.Context(), slog.New(slog.DiscardHandler))

	// A rotation writes the certificate and the key separately, so
	// a read landing between the two finds a pair that does not match.
	got, _ := c.GetClientCertificate(nil)
	if got != tlsCert {
		t.Error("got a swapped certificate, want the last good pair kept")
	}
}

func TestReloadSwapsTheKeyPair(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeKeypair(t, dir)
	c := mustLoad(t, cert, key)

	tlsCert, _ := c.GetClientCertificate(nil)

	rotatedCert, rotatedKey := newKeyPair(t)
	writeFile(t, dir, filepath.Base(cert), rotatedCert)
	writeFile(t, dir, filepath.Base(key), rotatedKey)
	c.reload(t.Context(), slog.New(slog.DiscardHandler))

	got, _ := c.GetClientCertificate(nil)
	if got == tlsCert {
		t.Error("got the initial certificate, want the rotated one")
	}
}

// The one test that goes through fsnotify, so it pays real
// time for the debounce window rather than running against
// a fake clock.
func TestCertificateRotatesThroughMountedVolume(t *testing.T) {
	cert, key := newKeyPair(t)

	dir := mountedDir(t, map[string]string{
		"tls.crt": cert,
		"tls.key": key,
	})
	c := mustLoad(t, filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	tlsCert, _ := c.GetClientCertificate(nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	logger, ready := signalOnFirstLog()
	done := make(chan struct{})

	go func() {
		defer close(done)
		if err := c.Watch(ctx, logger); err != nil {
			t.Errorf("got error %v, want the watch to stop cleanly", err)
		}
	}()
	// The loop takes its content baseline just before it logs,
	// so a swap landing any earlier becomes that baseline and
	// goes undetected.
	<-ready

	rotatedCert, rotatedKey := newKeyPair(t)
	swapTimestampDir(t, dir, map[string]string{
		"tls.crt": rotatedCert,
		"tls.key": rotatedKey,
	})
	eventually(t, func() bool {
		got, _ := c.GetClientCertificate(nil)
		return got != tlsCert
	})
	cancel()
	<-done
}

func mustLoad(t *testing.T, certFile, keyFile string) *Certificate {
	t.Helper()

	c, err := NewCertificate(certFile, keyFile)
	if err != nil {
		t.Fatalf("failed to load keypair: %v", err)
	}
	return c
}

type firstLogHandler struct {
	slog.Handler
	ready chan struct{}
	once  sync.Once
}

func (h *firstLogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *firstLogHandler) Handle(context.Context, slog.Record) error {
	h.once.Do(func() { close(h.ready) })
	return nil
}

// signalOnFirstLog returns a logger and a channel closed when the
// first record is emitted. A Watcher's watch loop logs once it is
// running, which is after the fsnotify watch is registered, so it
// serves as a readiness signal that no sleep can provide reliably.
func signalOnFirstLog() (*slog.Logger, <-chan struct{}) {
	h := &firstLogHandler{
		Handler: slog.DiscardHandler,
		ready:   make(chan struct{}),
	}
	return slog.New(h), h.ready
}

// eventually waits for cond to hold, well past the debounce window.
func eventually(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * debounceInterval)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the certificate was not reloaded before the deadline")
}

// writeKeypair writes a new TLS keypair into dir and returns the
// cert/key files paths.
func writeKeypair(t *testing.T, dir string) (string, string) {
	t.Helper()

	certPEM, keyPEM := newKeyPair(t)
	return writeFile(t, dir, "tls.crt", certPEM), writeFile(t, dir, "tls.key", keyPEM)
}

func newKeyPair(t *testing.T) (string, string) {
	t.Helper()

	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "kem-agent"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDer, err := x509.CreateCertificate(rand.Reader, &template, &template, &pk.PublicKey, pk)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	keyDer, err := x509.MarshalPKCS8PrivateKey(pk)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	return encodePEM("CERTIFICATE", certDer), encodePEM("PRIVATE KEY", keyDer)
}

func encodePEM(blockType string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}))
}
