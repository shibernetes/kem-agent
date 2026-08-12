package sink

import (
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/config/units"
)

func TestZeroLimits(t *testing.T) {
	type config interface {
		Validate() error
	}
	timeout := units.Duration(5 * time.Second)

	// A zero limit means "no limit" on a batch and nothing at all on
	// a queue, which are the two readings that must not be confused.
	cases := map[string]struct {
		cfg   config
		valid bool
	}{
		"batch bounded by events":  {BatchConfig{MaxEvents: 512, Timeout: timeout}, true},
		"batch bounded by bytes":   {BatchConfig{MaxBytes: 4 << 20, Timeout: timeout}, true},
		"batch bounded by neither": {BatchConfig{Timeout: timeout}, false},
		"queue at one chunk":       {QueueConfig{MaxBytes: QueueChunkSize}, true},
		"queue below one chunk":    {QueueConfig{MaxBytes: QueueChunkSize - 1}, false},
		"queue unbounded":          {QueueConfig{}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.cfg.Validate()
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the config accepted", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the config rejected")
			}
		})
	}
}

func TestDefaultsValidate(t *testing.T) {
	type config interface {
		Validate() error
	}
	cases := map[string]config{
		"batch": DefaultBatchConfig(),
		"queue": DefaultQueueConfig(),
		"retry": DefaultRetryConfig(),
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err != nil {
				t.Errorf("the default was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestDefaultBatchConfig(t *testing.T) {
	want := BatchConfig{
		MaxEvents: 512,
		MaxBytes:  4 << 20,
		Timeout:   units.Duration(5 * time.Second),
	}
	if got := DefaultBatchConfig(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDefaultQueueConfig(t *testing.T) {
	want := QueueConfig{MaxBytes: 32 << 20}

	if got := DefaultQueueConfig(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	want := RetryConfig{
		Enabled:         true,
		Timeout:         units.Duration(30 * time.Second),
		InitialInterval: units.Duration(500 * time.Millisecond),
		MaxInterval:     units.Duration(10 * time.Second),
		Multiplier:      1.5,
	}
	if got := DefaultRetryConfig(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
