package filter

import (
	"github.com/google/cel-go/interpreter"

	"github.com/shibernetes/kem-agent/event"
)

const (
	// variableName is the root variable every expression filters
	// on, as in event.type == 'Warning'.
	variableName = "event"
)

var _ interpreter.Activation = (*activation)(nil)

// activation supplies the event an expression is evaluated against,
// and is the only input a compiled rule reads. The name is cel-go's
// own, where an activation is what binds variables for a program.
//
// One is reused for every event a dispatch goroutine handles, so it
// belongs to that goroutine alone. Being read-only during evaluation
// is what lets one instance serve every rule.
type activation struct {
	root eventVal
}

// reset binds the activation to another event, dropping everything
// resolved for the previous one.
func (a *activation) reset(ev *event.Event) {
	a.root.reset(ev)
}

// ResolveName implements the [interpreter.Activation] interface.
// It returns the bound event for the root variable, and false for
// any other name.
func (a *activation) ResolveName(name string) (any, bool) {
	if name == variableName {
		return &a.root, true
	}
	return nil, false
}

// Parent implements the [interpreter.Activation] interface.
// An activation has no enclosing scope.
func (a *activation) Parent() interpreter.Activation {
	return nil
}
