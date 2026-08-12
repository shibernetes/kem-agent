package sink

import (
	"errors"
	"time"
)

var (
	// ErrSendTimeout signals a delivery attempt that reached the deadline.
	// The drainer wraps each attempt's context with it as the cancellation
	// cause, so a sink reporting a deadline returns an error wrapping it
	// rather than a bare context error.
	ErrSendTimeout = errors.New("send timed out")

	// ErrOversized signals a payload the destination rejected because of
	// its size. Resending the same bytes cannot succeed, so a sink returns
	// an error wrapping it and the caller handles it according to its
	// configuration.
	ErrOversized = errors.New("payload too large")

	// ErrRejected signals an event an [EventSink] declined to record, such
	// as a series refused under a cardinality limit. It is an accounting
	// outcome rather than a delivery failure, and is never retried.
	ErrRejected = errors.New("event rejected")

	// ErrPermanent signals a delivery failure that no retry can resolve,
	// such as a payload the destination rejects as malformed or unauthorized.
	// A sink returns an error wrapping it so that the caller drops the
	// payload at once instead of retrying.
	ErrPermanent = errors.New("permanent delivery failure")

	// ErrNotOpen signals a delivery attempted before the sink acquired its
	// destination. Nothing in the retry loop opens a sink, so the payload
	// is dropped rather than retried.
	ErrNotOpen = errors.New("sink is not open")

	// ErrClosed signals a delivery attempted after the sink released its
	// destination, which means a send outlived the stop sequence.
	ErrClosed = errors.New("sink is closed")
)

// RetryAfterError wraps a transient delivery error with a delay the
// destination asked the client to wait before retrying, taken from a
// Retry-After response header or from the RetryInfo of a gRPC status.
// The retry loop waits at least this long before the next attempt,
// still bounded by its own limits.
type RetryAfterError struct {
	Err   error
	Delay time.Duration
}

// NewRetryAfterError wraps err with the delay the destination asked for.
func NewRetryAfterError(err error, delay time.Duration) error {
	return &RetryAfterError{
		Err:   err,
		Delay: delay,
	}
}

// Error implements the error interface.
func (e *RetryAfterError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the wrapped transient error.
func (e *RetryAfterError) Unwrap() error {
	return e.Err
}

// A Result is the outcome of a delivery attempt.
type Result string

const (
	ResultSuccess   Result = "success"
	ResultRetryable Result = "retryable"
	ResultPermanent Result = "permanent"
	ResultTimeout   Result = "timeout"
	ResultOversized Result = "oversized"
)

// String implements the [fmt.Stringer] interface.
// It returns the outcome's label string.
func (r Result) String() string {
	return string(r)
}

// Classify reduces the error a [BatchSink] returned to the outcome
// recorded against the attempt. Anything it does not recognize is
// retryable, so an unclassified failure costs attempts rather than
// dropping a payload that a retry could have delivered.
func Classify(err error) Result {
	switch {
	case err == nil:
		return ResultSuccess
	case errors.Is(err, ErrOversized):
		return ResultOversized
	case errors.Is(err, ErrPermanent),
		errors.Is(err, ErrNotOpen),
		errors.Is(err, ErrClosed):
		return ResultPermanent
	case errors.Is(err, ErrSendTimeout):
		return ResultTimeout
	default:
		return ResultRetryable
	}
}
