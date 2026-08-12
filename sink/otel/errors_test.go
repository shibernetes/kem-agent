package otel

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/shibernetes/kem-agent/sink"
)

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		err  error
		want sink.Result
	}{
		"delivered":              {nil, sink.ResultSuccess},
		"canceled":               {status.Error(codes.Canceled, "canceled"), sink.ResultRetryable},
		"deadline exceeded":      {status.Error(codes.DeadlineExceeded, "too slow"), sink.ResultRetryable},
		"aborted":                {status.Error(codes.Aborted, "aborted"), sink.ResultRetryable},
		"out of range":           {status.Error(codes.OutOfRange, "out of range"), sink.ResultRetryable},
		"unavailable":            {status.Error(codes.Unavailable, "down"), sink.ResultRetryable},
		"data loss":              {status.Error(codes.DataLoss, "lost"), sink.ResultRetryable},
		"exhausted":              {status.Error(codes.ResourceExhausted, "too large"), sink.ResultOversized},
		"exhausted, recoverable": {retryableStatus(t, codes.ResourceExhausted, time.Second), sink.ResultRetryable},
		"invalid argument":       {status.Error(codes.InvalidArgument, "malformed"), sink.ResultPermanent},
		"unauthenticated":        {status.Error(codes.Unauthenticated, "no token"), sink.ResultPermanent},
		"permission denied":      {status.Error(codes.PermissionDenied, "forbidden"), sink.ResultPermanent},
		"unimplemented":          {status.Error(codes.Unimplemented, "no such method"), sink.ResultPermanent},
		"internal":               {status.Error(codes.Internal, "oops"), sink.ResultPermanent},
		"unknown":                {status.Error(codes.Unknown, "unknown"), sink.ResultPermanent},
		"no status":              {errors.New("connection refused"), sink.ResultRetryable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := sink.Classify(classify(t.Context(), tc.err)); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// TestClassifyReportsDeadline asserts that a context deadline set
// with the send timeout as its cause is reported as a timeout, not
// as a cancellation that can be retried.
func TestClassifyReportsDeadline(t *testing.T) {
	err := classify(timedOutContext(t), status.Error(codes.Canceled, "canceled"))

	if got := sink.Classify(err); got != sink.ResultTimeout {
		t.Errorf("got %s, want %s", got, sink.ResultTimeout)
	}
}

// TestClassifyReportsRequestedDelay asserts that a delay the collector
// asked for reaches the retry loop.
func TestClassifyReportsRequestedDelay(t *testing.T) {
	err := classify(t.Context(), retryableStatus(t, codes.Unavailable, 30*time.Second))

	retry, ok := errors.AsType[*sink.RetryAfterError](err)
	if !ok {
		t.Fatalf("got type %T, want a *sink.RetryAfterError", err)
	}
	if retry.Delay != 30*time.Second {
		t.Errorf("got %s delay, want 30s", retry.Delay)
	}
}

// TestClassifyAcceptsRetryInfoWithoutDelay asserts that a RetryInfo
// marks a failure recoverable even when it carries no delay.
func TestClassifyAcceptsRetryInfoWithoutDelay(t *testing.T) {
	err := classify(t.Context(), retryableStatus(t, codes.ResourceExhausted, 0))

	if got := sink.Classify(err); got != sink.ResultRetryable {
		t.Errorf("got %s, want %s", got, sink.ResultRetryable)
	}
	if _, ok := errors.AsType[*sink.RetryAfterError](err); ok {
		t.Errorf("got a requested delay, want none: %v", err)
	}
}

// TestClassifyReadsOnlyRetryInfo asserts that another status detail
// does not report an exhausted resource as recoverable.
func TestClassifyReadsOnlyRetryInfo(t *testing.T) {
	st := status.New(codes.ResourceExhausted, "too large")

	st, err := st.WithDetails(&errdetails.DebugInfo{Detail: "over the limit"})
	if err != nil {
		t.Fatalf("failed to add details: %v", err)
	}
	if got := sink.Classify(classify(t.Context(), st.Err())); got != sink.ResultOversized {
		t.Errorf("got %s, want %s", got, sink.ResultOversized)
	}
}

func retryableStatus(t *testing.T, code codes.Code, delay time.Duration) error {
	t.Helper()

	st := status.New(code, "retry later")

	st, err := st.WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(delay)})
	if err != nil {
		t.Fatalf("failed to attach retry info: %v", err)
	}
	return st.Err()
}
