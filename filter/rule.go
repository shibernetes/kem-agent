package filter

import (
	"context"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types/ref"
)

// Rule represents a compiled filter expression.
//
// A rule holds no state of its own once compiled, and every
// dispatch goroutine evaluates the same one, so it is shared
// rather than copied.
type Rule struct {
	program       cel.Program
	expr          string
	interruptible bool
}

// Expr returns the expression the rule was compiled from,
// as it was written in the configuration.
func (r *Rule) Expr() string {
	return r.expr
}

// eval evaluates the rule against an activation. Only an expression that
// can be interrupted uses the given context.
func (r *Rule) eval(ctx context.Context, act any) (ref.Val, *cel.EvalDetails, error) {
	if r.interruptible {
		return r.program.ContextEval(ctx, act)
	}
	return r.program.Eval(act)
}

// Set is the filters of one pipeline, combined with AND.
// An empty set matches every event.
type Set []*Rule
