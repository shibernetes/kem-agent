package configwatcher

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"sync/atomic"
)

// A Certificate watches a certificate and key file for changes.
// It always returns the cached keypair, reloading it in place as
// the files rotate.
type Certificate struct {
	certFile string
	keyFile  string
	current  atomic.Pointer[tls.Certificate]
}

// NewCertificate returns a new Certificate watching the given
// certificate and key.
func NewCertificate(certFile, keyFile string) (*Certificate, error) {
	c := &Certificate{
		certFile: certFile,
		keyFile:  keyFile,
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load keypair: %w", err)
	}
	c.current.Store(&cert)

	return c, nil
}

// Watch starts the watch on the certificate and key files, until
// ctx is canceled.
func (c *Certificate) Watch(ctx context.Context, logger *slog.Logger) error {
	w, err := New(func() { c.reload(ctx, logger) }, c.certFile, c.keyFile)
	if err != nil {
		return err
	}
	return w.Watch(ctx, logger)
}

// GetClientCertificate fetches the currently loaded keypair, and satisfies
// the hook of the same name on [tls.Config].
func (c *Certificate) GetClientCertificate(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return c.current.Load(), nil
}

// reload reads the certificate and key files from disk, parses them, and
// updates the current keypair. The one already loaded is kept when the two
// do not make a pair, which a read landing between the separate writes of
// a rotation finds.
func (c *Certificate) reload(ctx context.Context, logger *slog.Logger) {
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "failed to reload keypair", slog.Any("error", err))
		return
	}
	c.current.Store(&cert)
	logger.LogAttrs(ctx, slog.LevelInfo, "keypair reloaded", slog.String("certFile", c.certFile))
}
