package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/sink"
)

var (
	_ sink.BatchSink = (*Sink)(nil)
	_ sink.Opener    = (*Sink)(nil)
)

// errMalformedPayload reports a payload that ends in the middle
// of a frame, which only a faulty encoder can produce.
var errMalformedPayload = errors.New("payload ends in the middle of a frame")

// Sink delivers each batch to a Kafka topic, as one record per event.
type Sink struct {
	name     string
	cfg      Config
	logger   *slog.Logger
	headers  []kgo.RecordHeader
	client   *kgo.Client
	encoder  sink.Encoder
	framer   sink.Framer
	closed   atomic.Bool
	records  []kgo.Record
	pointers []*kgo.Record
	indexes  map[*kgo.Record]int
	batchID  sink.BatchID
	settled  []bool
	refused  int
}

// New returns a sink producing records to a Kafka topic.
// The client is built by Open, so creating the sink performs no I/O.
func New(name string, cfg Config, logger *slog.Logger, meta identity.AgentMetadata) *Sink {
	return &Sink{
		name:    name,
		cfg:     cfg,
		logger:  logger,
		headers: recordHeaders(meta),
		encoder: encoder{key: cfg.MessageKey},
		framer:  framer{},
		indexes: make(map[*kgo.Record]int),
	}
}

// Name implements the [sink.Sink] interface.
func (s *Sink) Name() string {
	return s.name
}

// Encoder implements the [sink.BatchSink] interface.
func (s *Sink) Encoder() sink.Encoder {
	return s.encoder
}

// Framer implements the [sink.BatchSink] interface.
func (s *Sink) Framer() sink.Framer {
	return s.framer
}

// Open implements the [sink.Opener] interface.
func (s *Sink) Open(context.Context) error {
	if s.client != nil {
		return nil
	}
	opts, err := clientOptions(s.cfg, s.logger)
	if err != nil {
		return fmt.Errorf("sink/kafka: %w", err)
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("sink/kafka: failed to create client: %w", err)
	}
	s.client = client

	return nil
}

// Send implements the [sink.BatchSink] interface.
// It produces one record per frame of the payload, and reports how many
// records the brokers refused. A retry of the same batch produces only
// the records that are still pending.
func (s *Sink) Send(ctx context.Context, payload []byte, id sink.BatchID) (int, error) {
	switch {
	case s.closed.Load():
		return 0, fmt.Errorf("%w: %s", sink.ErrClosed, s.cfg.Topic)
	case s.client == nil:
		return 0, fmt.Errorf("%w: %s", sink.ErrNotOpen, s.cfg.Topic)
	}
	// The records point into the payload, which the drainer reuses,
	// so they are cleared once the produce returns.
	defer s.clearRecords()

	if err := s.readRecords(payload); err != nil {
		return 0, fmt.Errorf("%w: %w", sink.ErrPermanent, err)
	}
	// Retries keep the identifier of their batch, so a new one means a new batch.
	if id != s.batchID {
		s.startNewBatch(id)
	}
	s.collectPendingRecords()

	var pending error

	// The results don't come back in the order the records were sent,
	// so the indexes map each record to its original position.
	for _, r := range s.client.ProduceSync(ctx, s.pointers...) {
		switch classify(r.Err) {
		case recordDelivered:
			s.settled[s.indexes[r.Record]] = true
		case recordRefused:
			s.settled[s.indexes[r.Record]] = true
			s.refused++
		case recordFatal:
			return 0, fmt.Errorf("%w: %w", sink.ErrPermanent, r.Err)
		case recordPending:
			if pending == nil {
				pending = r.Err
			}
		}
	}
	if pending != nil {
		return 0, retryable(ctx, pending)
	}
	return s.refused, nil
}

// Shutdown implements the [sink.Sink] interface.
func (s *Sink) Shutdown(context.Context) error {
	s.closed.Store(true)

	if s.client != nil {
		s.client.Close()
	}
	return nil
}

// readRecords turns each frame of the payload into a record.
// The key and value of a record point into the payload rather than copying it.
func (s *Sink) readRecords(payload []byte) error {
	for len(payload) > 0 {
		key, value, rest, ok := readFrame(payload)
		if !ok {
			return errMalformedPayload
		}
		// The timestamp isn't set here, because the client's delivery timeout
		// counts from it. Using the event's own time would make old events time
		// out straight away. The client sets it when it produces a record.
		s.records = append(s.records, kgo.Record{
			Key:     key,
			Value:   value,
			Headers: s.headers,
		})
		payload = rest
	}
	return nil
}

// startNewBatch resets the state to start a new batch.
func (s *Sink) startNewBatch(id sink.BatchID) {
	s.batchID, s.refused = id, 0
	s.settled = slices.Grow(s.settled[:0], len(s.records))[:len(s.records)]

	clear(s.settled)
}

// collectPendingRecords selects the records of the batch that aren't settled
// yet, and indexes each one by its position in the batch.
func (s *Sink) collectPendingRecords() {
	// The pointers are collected once every record is appended,
	// since growing the slice may move the records it contains.
	for i := range s.records {
		if !s.settled[i] {
			r := &s.records[i]
			s.pointers = append(s.pointers, r)
			s.indexes[r] = i
		}
	}
}

func (s *Sink) clearRecords() {
	clear(s.records)
	clear(s.pointers)
	clear(s.indexes)

	s.records, s.pointers = s.records[:0], s.pointers[:0]
}
