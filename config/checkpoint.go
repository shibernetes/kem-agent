package config

import (
	"strings"
	"time"

	"github.com/goccy/go-yaml/ast"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/checkpoint/configmap"
	"github.com/shibernetes/kem-agent/checkpoint/file"
	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/internal/identity"
)

const (
	defaultSaveInterval = units.Duration(15 * time.Second)
)

var storeTypes = []string{configmap.TypeName, file.TypeName}

// Checkpoint configures where and how often the watch read positions
// are persisted.
type Checkpoint struct {
	checkpoint.Config `yaml:",inline"`
	Store             Component `yaml:"store"`
}

// DefaultCheckpointConfig returns the default checkpoint configuration.
func DefaultCheckpointConfig() Checkpoint {
	return Checkpoint{
		SaveInterval: defaultSaveInterval,
	}
}

// Validate validates the configuration.
// The store's implementation settings are validated with the other components.
func (c Checkpoint) Validate() error {
	return c.Config.Validate()
}

// resolveStore decodes the store config block into its own config type.
// The parent node positions the two failures raised before a store type
// is known, neither of which has a block of its own to point at.
func (c *Checkpoint) resolveStore(parent ast.Node) error {
	switch {
	case c.Store.Node() == nil:
		return diag.Atf(parent, "checkpoint: store is required")
	case c.Store.Type == "":
		return diag.Atf(c.Store.Node(), "checkpoint: store: type is required")
	}
	switch c.Store.Type {
	case file.TypeName:
		cfg := file.DefaultConfig()
		return c.Store.Decode(&cfg)
	case configmap.TypeName:
		cfg := configmap.DefaultConfig()
		cfg.Namespace = identity.Resolve("").Namespace

		return c.Store.Decode(&cfg)
	default:
		return diag.Atf(c.Store.Node(),
			"checkpoint: store: unknown type %q, allowed values are %s",
			c.Store.Type, strings.Join(storeTypes, ", "))
	}
}
