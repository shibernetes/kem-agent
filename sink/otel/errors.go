package otel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shibernetes/kem-agent/sink"
)

// retryableCodes lists the status codes the OTLP specification marks as
// retryable. RESOURCE_EXHAUSTED is not among them, since it is retryable
// only when the server says it can recover, which classify reads
// separately.
// https://opentelemetry.io/docs/specs/otlp/#otlpgrpc-response
var retryableCodes = []codes.Code{
	codes.Canceled,
	codes.DeadlineExceeded,
	codes.Aborted,
	codes.OutOfRange,
	codes.Unavailable,
	codes.DataLoss,
}

// classify turns an export failure into a delivery error, following the
// retryability rules of the OTLP specification. A status that is not
// retryable wraps [sink.ErrPermanent]. An error without a status is
// returned unchanged, and the caller retries it.
func classify(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	// Our own deadline arrives as a plain cancellation, so the context
	// cause is the only way to tell it apart.
	if cause := context.Cause(ctx); errors.Is(cause, sink.ErrSendTimeout) {
		return fmt.Errorf("%w: %w", sink.ErrSendTimeout, err)
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	delay, recoverable := retryDelay(st)

	switch code := st.Code(); {
	case code == codes.ResourceExhausted && !recoverable:
		// A message the receiver refused for its size. A throttling
		// intermediary sends the same code with a RetryInfo, which
		// is what distinguishes the two.
		return fmt.Errorf("%w: %w", sink.ErrOversized, err)
	case code == codes.ResourceExhausted:
	case !slices.Contains(retryableCodes, code):
		return fmt.Errorf("%w: %w", sink.ErrPermanent, err)
	}
	if delay > 0 {
		return sink.NewRetryAfterError(err, delay)
	}
	return err
}

// retryDelay returns the delay the server asked for, and whether it sent
// a RetryInfo, which indicates the server can recover from the failure.
func retryDelay(st *status.Status) (time.Duration, bool) {
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.RetryInfo); ok {
			return info.GetRetryDelay().AsDuration(), true
		}
	}
	return 0, false
}
