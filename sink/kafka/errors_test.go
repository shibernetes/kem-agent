package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"testing"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/shibernetes/kem-agent/sink"
)

// TestClassify asserts the result of a record for each kind of error,
// including a cause that the client reports for a record that timed out
// before the metadata of its topic was loaded.
func TestClassify(t *testing.T) {
	cases := map[string]struct {
		err  error
		want recordResult
	}{
		"stored":                    {nil, recordDelivered},
		"record too large":          {kerr.MessageTooLarge, recordRefused},
		"corrupt record":            {kerr.CorruptMessage, recordRefused},
		"wrong password":            {afterTimeout(kerr.SaslAuthenticationFailed), recordFatal},
		"topic not authorized":      {kerr.TopicAuthorizationFailed, recordFatal},
		"missing topic":             {kerr.UnknownTopicOrPartition, recordFatal},
		"untrusted certificate":     {afterTimeout(&tls.CertificateVerificationError{}), recordFatal},
		"TLS against plaintext":     {afterTimeout(&kgo.ErrFirstReadEOF{}), recordFatal},
		"leader change":             {kerr.NotLeaderForPartition, recordPending},
		"connection refused":        {afterTimeout(errors.New("connection refused")), recordPending},
		"attempt deadline":          {context.DeadlineExceeded, recordPending},
		"unlisted non-retriable":    {kerr.UnknownServerError, recordPending},
		"no partition after lookup": {fmt.Errorf("no partitions available, last err: %w", kerr.LeaderNotAvailable), recordPending},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := classify(tc.err); got != tc.want {
				t.Errorf("got result %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRetryableMarksDeadline asserts that pending records report
// a send timeout only when the attempt reached its deadline.
func TestRetryableMarksDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeoutCause(context.Background(), 0, sink.ErrSendTimeout)
	defer cancel()
	<-ctx.Done()

	if err := retryable(ctx, context.DeadlineExceeded); !errors.Is(err, sink.ErrSendTimeout) {
		t.Errorf("got %v at the deadline, want a send timeout", err)
	}
	if err := retryable(context.Background(), kerr.NotLeaderForPartition); errors.Is(err, sink.ErrSendTimeout) {
		t.Errorf("got %v before the deadline, want no send timeout", err)
	}
}

// afterTimeout wraps an error the way the client reports the last cause
// of a record that timed out before the metadata of its topic was loaded.
func afterTimeout(err error) error {
	return fmt.Errorf("%w, last err: %w", kgo.ErrRecordTimeout, err)
}
