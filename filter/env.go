package filter

import (
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/ext"
	"github.com/google/cel-go/interpreter"
	"k8s.io/apiserver/pkg/cel/library"
)

const (
	// perExpressionCostLimit caps the runtime cost of one expression,
	// and is what names the filter responsible for an overrun.
	// Roughly ten milliseconds of evaluation.
	perExpressionCostLimit = 100_000

	// perEventCostLimit caps the total runtime cost of every expression
	// an event is evaluated against, across all of its pipelines. It is
	// checked between expressions, so an event is bounded by this plus
	// one expression, roughly a tenth of a second.
	perEventCostLimit = 1_000_000

	// interruptCheckFrequency is the number of iterations a comprehension
	// runs before checking whether it has been interrupted, which bounds
	// how far past its deadline one runs. Nothing interrupts a single
	// function call, however long it takes.
	interruptCheckFrequency = 10
)

// Env is the CEL environment every filter is compiled in, built once at
// config load and reused for every compilation. Its language settings
// match the Kubernetes APIServer's base environment, so a filter behaves
// like the expressions of an admission policy or a validation rule.
//
// The surface is fixed and compiled in rather than configurable, and each
// library is pinned to a specific version, so an upgrade cannot widen or
// narrow what an expression may use.
//
// An expression one agent accepts is therefore accepted by any other
// running the same version. Adding a library later is safe, while removing
// one breaks configurations that already run.
type Env struct {
	env *cel.Env
}

// NewEnv builds the filter environment.
func NewEnv() (*Env, error) {
	provider, err := newProvider()
	if err != nil {
		return nil, err
	}
	celEnv, err := cel.NewEnv(
		cel.CustomTypeProvider(provider),
		cel.Variable(variableName, eventType),

		cel.HomogeneousAggregateLiterals(),
		cel.EagerlyValidateDeclarations(true),
		cel.DefaultUTCTimeZone(true),
		cel.CrossTypeNumericComparisons(true),
		cel.OptionalTypes(),
		cel.CostEstimatorOptions(checker.PresenceTestHasCost(false)),
		cel.ASTValidators(
			cel.ValidateDurationLiterals(),
			cel.ValidateTimestampLiterals(),
			cel.ValidateRegexLiterals(),
			cel.ValidateHomogeneousAggregateLiterals(),
		),

		// Strings is pinned at 5 because that is where its
		// functions start being priced by argument size rather
		// than as plain calls.
		ext.Strings(ext.StringsVersion(5)),
		ext.Sets(ext.SetsVersion(0)),
		ext.TwoVarComprehensions(ext.TwoVarComprehensionsVersion(0)),

		// Bindings at version 0 leaves out cel.@block, an optimizer
		// internal that no expression writes.
		ext.Bindings(ext.BindingsVersion(0)),

		// Semver at version 1 adds the normalizing forms, which is
		// what allows a short label value such as 1.0 to parse.
		library.SemverLib(library.SemverVersion(1)),
		library.Regex(),
		library.IP(),
		library.CIDR(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build CEL environment: %w", err)
	}
	return &Env{env: celEnv}, nil
}

// Compile checks one expression against the event schema and turns it
// into a rule. The expression must yield a bool, and one typed dyn is
// accepted but checked again when it is evaluated.
func (e *Env) Compile(expr string) (*Rule, error) {
	ast, iss := e.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, newCompileError(expr, iss)
	}
	if kind := ast.OutputType().Kind(); kind != types.BoolKind && kind != types.DynKind {
		return nil, newCompileErrorf(expr, "must yield bool, got %s", ast.OutputType())
	}
	// Every rule tracks its cost.
	// Working out which expressions need tracking is more expensive
	// than tracking them all.
	program, err := e.env.Program(ast,
		cel.EvalOptions(cel.OptOptimize, cel.OptTrackCost),
		cel.CostLimit(perExpressionCostLimit),
		cel.CostTrackerOptions(interpreter.PresenceTestHasCost(false)),
		cel.InterruptCheckFrequency(interruptCheckFrequency),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build program for expression %q: %w", expr, err)
	}
	return &Rule{
		program:       program,
		expr:          expr,
		interruptible: hasComprehension(ast),
	}, nil
}

// hasComprehension reports whether an expression folds a comprehension,
// which is where cel-go checks whether an evaluation was interrupted.
func hasComprehension(ast *cel.Ast) bool {
	nav := celast.NavigateAST(ast.NativeRep())
	if nav.Kind() == celast.ComprehensionKind {
		return true
	}
	return len(celast.MatchDescendants(nav, celast.KindMatcher(celast.ComprehensionKind))) > 0
}
