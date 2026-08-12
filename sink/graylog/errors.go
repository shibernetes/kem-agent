package graylog

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/netconn"
)

// classify turns a connection or write error into a delivery error.
// A GELF TCP input sends no application-level acknowledgement, so failures
// are connection-level and transient, returned as-is. A TLS certificate
// verification failure is deterministic and wraps [sink.ErrPermanent].
func classify(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	// A deadline reached mid-write surfaces as a transport error, which
	// carries nothing to recognize it by. The cause the caller wrapped the
	// context with is what distinguishes it.
	if cause := context.Cause(ctx); errors.Is(cause, sink.ErrSendTimeout) {
		return fmt.Errorf("%w: %w", sink.ErrSendTimeout, err)
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return fmt.Errorf("%w: %w", sink.ErrPermanent, err)
	}
	if errors.Is(err, netconn.ErrClosed) {
		return fmt.Errorf("%w: %w", sink.ErrClosed, err)
	}
	return err
}
