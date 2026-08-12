package checkpoint

import (
	"context"
	"errors"
)

var (
	// ErrNotFound is returned by a [Store] holding no state.
	ErrNotFound = errors.New("checkpoint state not found")

	// ErrCorrupt is returned by a [Store] whose state cannot be decoded.
	// It is distinct from one that cannot be reached, where the positions
	// may be intact and resuming from nothing would replay for no reason.
	ErrCorrupt = errors.New("checkpoint state is unreadable")
)

// A Store persists a checkpoint [State].
type Store interface {
	// Load returns the stored state. The returned error is [ErrNotFound]
	// when there is none, and [ErrCorrupt] when it cannot be decoded.
	Load(context.Context) (State, error)

	// Save replaces the stored state with the given one. It never merges,
	// so a watch removed from the state is forgotten.
	Save(context.Context, State) error
}

// State stores how far each watch has been read, which a checkpointer
// reads and writes as a single unit. Resource versions are stored as
// the strings the APIServer returns.
type State struct {
	// Watches maps a watch key, a namespace or "" for the all-namespaces
	// watch, to the furthest position its stream was read to.
	Watches map[string]string `json:"watches"`
}
