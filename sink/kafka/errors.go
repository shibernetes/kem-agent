package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"slices"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/shibernetes/kem-agent/sink"
)

// refusedErrors lists the errors that a broker returns for a record it will
// never accept, so that the record isn't sent again.
var refusedErrors = []error{
	kerr.MessageTooLarge,
	kerr.RecordListTooLarge,
	kerr.InvalidRecord,
	kerr.InvalidTimestamp,

	// Kafka marks it retriable, but franz-go never retries it on produce,
	// since it reports a record the broker can't read, such as a record
	// without a key on a compacted topic.
	kerr.CorruptMessage,
}

// fatalErrors lists the errors that concern the connection or the topic
// rather than a single record.
var fatalErrors = []error{
	kerr.SaslAuthenticationFailed,
	kerr.UnsupportedSaslMechanism,
	kerr.IllegalSaslState,
	kerr.TopicAuthorizationFailed,
	kerr.ClusterAuthorizationFailed,
	kerr.InvalidRequiredAcks,
	kerr.InvalidTopicException,

	// franz-go only reports a missing topic after several metadata
	// requests have failed to find it.
	kerr.UnknownTopicOrPartition,
	kerr.UnknownTopicID,
}

// A recordResult classifies the produce error of a record, which decides
// whether the record is delivered, refused, retried, or fails the batch.
type recordResult string

const (
	recordDelivered recordResult = "delivered"
	recordRefused   recordResult = "refused"
	recordPending   recordResult = "pending"
	recordFatal     recordResult = "fatal"
)

// classify returns the result of a record from the error that the client
// reported for it.
func classify(err error) recordResult {
	switch {
	case err == nil:
		return recordDelivered
	case isFatal(err):
		return recordFatal
	case matchesAny(err, refusedErrors):
		return recordRefused
	}
	return recordPending
}

// retryable returns the error that makes the drainer retry a batch whose
// records weren't all produced. If the attempt ran out of time, the error
// also reports a send timeout.
func retryable(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), sink.ErrSendTimeout) {
		return fmt.Errorf("%w: %w", sink.ErrSendTimeout, err)
	}
	return err
}

// isFatal reports whether an error concerns the connection or the topic,
// and lasts until the configuration changes.
func isFatal(err error) bool {
	if matchesAny(err, fatalErrors) {
		return true
	}
	// A bad certificate or a TLS or SASL mismatch fails every connection.
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}
	_, ok := errors.AsType[*kgo.ErrFirstReadEOF](err)
	return ok
}

// matchesAny reports whether err matches any of the target errors.
func matchesAny(err error, targets []error) bool {
	return slices.ContainsFunc(targets, func(target error) bool {
		return errors.Is(err, target)
	})
}
