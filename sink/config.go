package sink

import (
	"time"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/units"
)

const (
	// QueueChunkSize is the chunks size a queue writes its frames in.
	// It is also the smallest ceiling a queue may use, since a queue
	// allocates a whole chunk as soon as it holds anything.
	//
	// It matches Go's largest small-object size class (67), so a chunk
	// is allocated from a size-class span rather than as a large object.
	QueueChunkSize = 32 << 10 // 32 KiB
)

const (
	defaultBatchMaxEvents = 512
	defaultBatchMaxBytes  = 4 << 20 // 4 MiB
	defaultBatchTimeout   = units.Duration(5 * time.Second)
)

const (
	defaultRetryTimeout         = units.Duration(30 * time.Second)
	defaultRetryInitialInterval = units.Duration(500 * time.Millisecond)
	defaultRetryMaxInterval     = units.Duration(10 * time.Second)
	defaultRetryMultiplier      = 1.5
)

const (
	defaultQueueMaxBytes = 32 << 20 // 32 MiB
)

// Config defines the configuration for a [Sink].
// Each factory supplies its own type rather than a shared one, so that
// a strict decode covers the whole block in one pass. An implementation
// validates it on its own, so that anything a factory would reject is
// rejected before the factory runs.
type Config any

// A FieldRef represents an event field read by a sink implementation.
type FieldRef struct {
	Field string
	Path  string
}

// DrainerConfig carries the settings the delivery path reads from a
// sink's own configuration block.
type DrainerConfig struct {
	Batch       BatchConfig
	Queue       QueueConfig
	Retry       RetryConfig
	SendTimeout units.Duration
}

// Drainable defines an optional interface for a sink's configuration
// to implement, so that the delivery path reads its settings without
// knowing the concrete type. Only the configuration of a [BatchSink]
// carries them.
type Drainable interface {
	DrainerConfig() DrainerConfig
}

// BatchConfig configures how a batch sink groups frames into one
// payload. A batch closes on whichever limit binds first, so a limit
// left at zero is simply ignored.
type BatchConfig struct {
	MaxEvents int            `yaml:"max_events,omitempty"`
	MaxBytes  units.Bytes    `yaml:"max_bytes,omitempty"`
	Timeout   units.Duration `yaml:"timeout,omitempty"`
}

// DefaultBatchConfig returns the default batch config.
func DefaultBatchConfig() BatchConfig {
	return BatchConfig{
		MaxEvents: defaultBatchMaxEvents,
		MaxBytes:  defaultBatchMaxBytes,
		Timeout:   defaultBatchTimeout,
	}
}

// Validate validates the configuration.
func (c BatchConfig) Validate() error {
	switch {
	case c.MaxEvents < 0:
		return diag.Pathf("max_events", "max_events must not be negative")
	case c.MaxBytes < 0:
		return diag.Pathf("max_bytes", "max_bytes must not be negative")
	case c.Timeout <= 0:
		return diag.Pathf("timeout", "timeout must be positive")
	case c.MaxEvents == 0 && c.MaxBytes == 0:
		// One of them may be zero. The limit that remains and the linger
		// timeout still bound the batch. With both zero, the timeout is
		// the only bound and a busy stream accumulates until it expires.
		return diag.Pathf("max_events", "max_events and max_bytes must not both be zero")
	}
	return nil
}

// QueueConfig defines the configuration for queueing the frames a sink
// has not yet delivered. The bytes limit is a ceiling rather than a hard
// reservation, so a queue's memory usage follows what it holds, and it
// drops its oldest frames only once it is full.
type QueueConfig struct {
	MaxBytes units.Bytes `yaml:"max_bytes,omitempty"`
}

// DefaultQueueConfig returns the default queue config.
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{MaxBytes: defaultQueueMaxBytes}
}

// Validate validates the configuration.
func (c QueueConfig) Validate() error {
	// Unlike a batch limit, zero does not mean "no limit" here.
	// An unbounded queue size implies unbounded memory, and dropping the
	// oldest frames is what keeps a stalled sink from stalling the agent.
	if c.MaxBytes < QueueChunkSize {
		return diag.Pathf("max_bytes", "max_bytes must be at least %d bytes", QueueChunkSize)
	}
	return nil
}

// RetryConfig configures how a sink's failed deliveries are reattempted.
type RetryConfig struct {
	Enabled         bool           `yaml:"enabled,omitempty"`
	Timeout         units.Duration `yaml:"timeout,omitempty"`
	InitialInterval units.Duration `yaml:"initial_interval,omitempty"`
	MaxInterval     units.Duration `yaml:"max_interval,omitempty"`
	Multiplier      float64        `yaml:"multiplier,omitempty"`
}

// DefaultRetryConfig returns the default retry config
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		Enabled:         true,
		Timeout:         defaultRetryTimeout,
		InitialInterval: defaultRetryInitialInterval,
		MaxInterval:     defaultRetryMaxInterval,
		Multiplier:      defaultRetryMultiplier,
	}
}

// Validate validates the configuration.
func (c RetryConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	switch {
	case c.Timeout <= 0:
		return diag.Pathf("timeout", "timeout must be positive")
	case c.InitialInterval <= 0:
		return diag.Pathf("initial_interval", "initial_interval must be positive")
	case c.MaxInterval < c.InitialInterval:
		return diag.Pathf("max_interval", "max_interval must be at least initial_interval")
	case c.Multiplier < 1:
		return diag.Pathf("multiplier", "multiplier must be at least 1")
	}
	return nil
}

// ValidateSharedConfigs validates the configs shared by every sink.
func ValidateSharedConfigs(batch BatchConfig, queue QueueConfig, retry RetryConfig) error {
	if err := batch.Validate(); err != nil {
		return diag.Prefix(err, "batch")
	}
	if err := queue.Validate(); err != nil {
		return diag.Prefix(err, "queue")
	}
	if err := retry.Validate(); err != nil {
		return diag.Prefix(err, "retry")
	}
	return nil
}
