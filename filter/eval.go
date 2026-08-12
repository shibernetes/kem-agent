package filter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/interpreter"

	"github.com/shibernetes/kem-agent/event"
)

const (
	// deadlineRefreshDivisor sets how long one deadline is reused
	// before a fresh one replaces it, as a fraction of the evaluation
	// timeout. A tenth of it means every event gets at least nine
	// tenths of the time it was allocated, whatever that timeout is.
	deadlineRefreshDivisor = 10
)

// errEventCostSpent signals that an event has already used up its
// whole cost limit before an expression could run.
var errEventCostSpent = errors.New("event cost limit spent")

// A Reason names the cause of an expression evaluation failure,
// which drops the event. The zero value means it succeeded.
type Reason string

const (
	ReasonEvalError   Reason = "eval_error"
	ReasonEvalCost    Reason = "eval_cost"
	ReasonEvalTimeout Reason = "eval_timeout"
)

// String implements the [fmt.Stringer] interface.
func (r Reason) String() string {
	return string(r)
}

// EvalState carries one event through every filter it is matched
// against, and bounds the evaluations by deadline and by cost.
type EvalState struct {
	act       activation
	timeout   time.Duration
	refresh   time.Duration
	ctx       context.Context
	ctxCancel context.CancelFunc
	ctxSince  time.Time
	cost      uint64
	rule      *Rule
	err       error
}

// NewEvalState returns a state. The timeout covers a whole event
// rather than a single expression, and must be positive.
func NewEvalState(timeout time.Duration) *EvalState {
	return &EvalState{
		timeout: timeout,
		refresh: timeout / deadlineRefreshDivisor,
	}
}

// Failure returns the last expression that failed to evaluate and
// the related error.
func (s *EvalState) Failure() (string, error) {
	if s.rule == nil {
		return "", s.err
	}
	return s.rule.expr, s.err
}

// Reset binds the state to a new event and clears everything the
// previous one left behind, its resolved fields and its cost alike.
// It must be called before matching an event.
func (s *EvalState) Reset(ev *event.Event) {
	s.act.reset(ev)
	s.cost = 0
	s.rule, s.err = nil, nil
	s.rotate()
}

// Match reports whether the bound event satisfies every rule in the set.
// A set with no rules matches every event.
//
// A non-zero reason reports that a rule failed to evaluate and names the
// cause. The event is dropped rather than simply not matched, and Failure
// can be used to get the expression and error behind it. ReasonEvalCost
// and ReasonEvalTimeout apply to the whole event rather than to this set.
func (s *EvalState) Match(set Set) (bool, Reason) {
	for _, r := range set {
		// The total is checked here rather than after the call,
		// because cel-go bounds one program at a time and cannot
		// see what the event has cost elsewhere.
		if s.cost >= perEventCostLimit {
			return s.fail(r, errEventCostSpent, ReasonEvalCost)
		}
		out, details, err := r.eval(s.ctx, &s.act)

		// An expression is charged for its cost whether it
		// answered, overran or was interrupted.
		s.charge(details)

		if err != nil {
			return s.fail(r, err, evalReason(err))
		}
		verdict, ok := out.(types.Bool)
		if !ok {
			err := fmt.Errorf("expression yielded %s, not bool", out.Type().TypeName())
			return s.fail(r, err, ReasonEvalError)
		}
		if !verdict {
			return false, ""
		}
	}
	return true, ""
}

// Close releases the evaluation deadline.
func (s *EvalState) Close() {
	if s.ctxCancel != nil {
		s.ctxCancel()
		s.ctx, s.ctxCancel = nil, nil
	}
}

// rotate replaces the evaluation context once it is older than the
// refresh interval, or once its deadline has already fired. Reusing
// one context across many events arms a single runtime timer per
// interval rather than one per event.
//
// The deadline bounds an evaluation and is not a shutdown mechanism,
// so its parent is the background context. Inheriting it from the agent
// context would expire every live deadline the moment the agent stops,
// recording a timeout against every watch.
func (s *EvalState) rotate() {
	now := time.Now()
	if s.ctx != nil && now.Sub(s.ctxSince) <= s.refresh && s.ctx.Err() == nil {
		return
	}
	if s.ctxCancel != nil {
		s.ctxCancel()
	}
	s.ctx, s.ctxCancel = context.WithTimeout(context.Background(), s.timeout)
	s.ctxSince = now
}

// charge adds one evaluation cost to the event's total.
func (s *EvalState) charge(details *cel.EvalDetails) {
	if details == nil {
		return
	}
	if cost := details.ActualCost(); cost != nil {
		s.cost += *cost
	}
}

// fail records what went wrong and reports the event as not matching.
func (s *EvalState) fail(r *Rule, err error, reason Reason) (bool, Reason) {
	s.rule, s.err = r, err
	return false, reason
}

// evalReason classifies an evaluation failure. cel-go reports a cost
// overrun as a cancellation that names the limit, and a passed deadline
// as an interrupt, which is what tells those two apart from each other
// and from an expression that is simply broken.
func evalReason(err error) Reason {
	if ce, ok := errors.AsType[interpreter.EvalCancelledError](err); ok &&
		ce.Cause == interpreter.CostLimitExceeded {
		return ReasonEvalCost
	}
	if errors.Is(err, interpreter.InterruptError{}) {
		return ReasonEvalTimeout
	}
	return ReasonEvalError
}
