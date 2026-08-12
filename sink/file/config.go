package file

import (
	"io/fs"
	"path/filepath"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/sink"
)

// TypeName is the value of the type key that selects this implementation.
const (
	TypeName = "file"
)

const (
	defaultMode = 0o644
)

var _ sink.Drainable = Config{}

// Config defines the configuration of the file sink.
// Mode is the permission a created file takes, written in octal form
// with a leading zero. An existing file keeps the permission it has.
type Config struct {
	Type  string           `yaml:"type"`
	Path  string           `yaml:"path"`
	Mode  fs.FileMode      `yaml:"mode,omitempty"`
	Batch sink.BatchConfig `yaml:"batch,omitempty"`
	Queue sink.QueueConfig `yaml:"queue,omitempty"`
	Retry sink.RetryConfig `yaml:"retry,omitempty"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		Type:  TypeName,
		Mode:  defaultMode,
		Batch: sink.DefaultBatchConfig(),
		Queue: sink.DefaultQueueConfig(),
		Retry: sink.DefaultRetryConfig(),
	}
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if err := diag.Required(c); err != nil {
		return err
	}
	if !filepath.IsAbs(c.Path) {
		return diag.Pathf("path", "path must be absolute: %q", c.Path)
	}
	// Anything outside the permission bits is a mode bit rather
	// than a permission, which is also where a mode written without
	// its leading zero lands, 644 in decimal being 01204.
	if c.Mode&^fs.ModePerm != 0 || c.Mode == 0 {
		return diag.Pathf("mode", "invalid mode %#o, must be a permission", c.Mode)
	}
	return sink.ValidateSharedConfigs(c.Batch, c.Queue, c.Retry)
}

// DrainerConfig implements the [sink.Drainable] interface.
// The sink writes synchronously, so it carries no send timeout.
func (c Config) DrainerConfig() sink.DrainerConfig {
	return sink.DrainerConfig{
		Batch: c.Batch,
		Queue: c.Queue,
		Retry: c.Retry,
	}
}
