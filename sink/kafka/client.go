package kafka

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"github.com/twmb/franz-go/plugin/kslog"
)

const (
	// maxMetadataMinAge is franz-go's default time between two metadata
	// refreshes, which it doesn't export.
	maxMetadataMinAge = 5 * time.Second
)

// clientOptions builds the options of the Kafka client from the configuration.
func clientOptions(cfg Config, log *slog.Logger) ([]kgo.Opt, error) {
	sendTimeout := time.Duration(cfg.SendTimeout)

	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.DefaultProduceTopic(cfg.Topic),
		kgo.ClientID(cfg.ClientID),
		kgo.RequiredAcks(acksOf(cfg.RequiredAcks)),
		kgo.ProducerBatchCompression(codecOf(cfg.Compression)),
		kgo.ProducerBatchMaxBytes(int32(cfg.MaxMessageBytes)), //nolint:gosec
		kgo.WithLogger(kslog.New(log)),

		// A record that can't be produced within this timeout fails with
		// ErrRecordTimeout before the attempt's deadline could fail it with
		// a bare context error. When the record failed to reach a broker,
		// ErrRecordTimeout wraps the connection error as its cause.
		kgo.RecordDeliveryTimeout(sendTimeout / 2),

		// A broker that can't complete a produce request within this timeout
		// answers with REQUEST_TIMED_OUT, before the attempt's deadline would
		// cancel the request in flight.
		kgo.ProduceRequestTimeout(sendTimeout / 2),

		// A batch that fails with NOT_LEADER_FOR_PARTITION is retried only
		// after the metadata update it triggers, and the client waits at
		// least this long between two metadata queries.
		kgo.MetadataMinAge(min(sendTimeout/5, maxMetadataMinAge)),

		// By default, the client refuses to cancel an in-flight record and
		// waits for its response, so idempotency holds. This lets context
		// cancellation fail it, at the risk of a duplicate once it's resent.
		kgo.AllowIdempotentProduceCancellation(),

		// Some brokers advertise client metrics, then reject them.
		kgo.DisableClientMetrics(),
	}
	// Idempotent writes require every in-sync replica to store a record.
	if cfg.RequiredAcks != RequiredAcksAll {
		opts = append(opts, kgo.DisableIdempotentWrite())
	}
	if cfg.AllowAutoTopicCreation {
		opts = append(opts, kgo.AllowAutoTopicCreation())
	}
	if cfg.Auth.SASL != nil {
		opts = append(opts, kgo.SASL(mechanismOf(*cfg.Auth.SASL)))
	}
	tlsConfig, err := cfg.TLS.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build TLS configuration: %w", err)
	}
	if tlsConfig != nil {
		opts = append(opts, kgo.DialTLSConfig(tlsConfig))
	}
	return opts, nil
}

// acksOf returns the client setting for the required acks.
func acksOf(a RequiredAcks) kgo.Acks {
	switch a {
	case RequiredAcksLeader:
		return kgo.LeaderAck()
	case RequiredAcksNone:
		return kgo.NoAck()
	}
	return kgo.AllISRAcks()
}

// codecOf returns the client codec for the compression.
func codecOf(c Compression) kgo.CompressionCodec {
	switch c {
	case CompressionGzip:
		return kgo.GzipCompression()
	case CompressionSnappy:
		return kgo.SnappyCompression()
	case CompressionLZ4:
		return kgo.Lz4Compression()
	case CompressionZstd:
		return kgo.ZstdCompression()
	}
	return kgo.NoCompression()
}

// mechanismOf returns the client SASL mechanism for the SASL config.
func mechanismOf(cfg SASLConfig) sasl.Mechanism {
	user, pass := cfg.Username, string(cfg.Password)

	switch cfg.Mechanism {
	case SASLMechanismSCRAMSHA256:
		return scram.Auth{User: user, Pass: pass}.AsSha256Mechanism()
	case SASLMechanismSCRAMSHA512:
		return scram.Auth{User: user, Pass: pass}.AsSha512Mechanism()
	}
	return plain.Auth{User: user, Pass: pass}.AsMechanism()
}
