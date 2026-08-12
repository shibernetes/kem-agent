package filter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/shibernetes/kem-agent/event"
)

const (
	evalTimeout = 250 * time.Millisecond
)

func TestMatchAccepts(t *testing.T) {
	env := newTestEnv(t)

	cases := map[string][]string{
		"an empty set":          nil,
		"one true expression":   {"event.type == 'Warning'"},
		"every expression true": {"event.type == 'Warning'", "event.regarding.kind == 'Pod'"},
	}
	for name, exprs := range cases {
		t.Run(name, func(t *testing.T) {
			state := newEvalState(t, evalTimeout, fullEvent())

			got, reason := state.Match(compileSet(t, env, exprs...))
			if reason != "" {
				t.Fatalf("got reason %q, want none", reason)
			}
			if !got {
				t.Error("the event did not match, want it to")
			}
		})
	}
}

func TestMatchRejects(t *testing.T) {
	env := newTestEnv(t)

	cases := map[string][]string{
		"one false expression":  {"event.type == 'Normal'"},
		"one false among three": {"event.type == 'Warning'", "event.namespace == 'other'", "event.reason == 'Failed'"},
	}
	for name, exprs := range cases {
		t.Run(name, func(t *testing.T) {
			state := newEvalState(t, evalTimeout, fullEvent())

			got, reason := state.Match(compileSet(t, env, exprs...))
			if reason != "" {
				t.Fatalf("got reason %q, want none", reason)
			}
			if got {
				t.Error("the event matched, want it not to")
			}
		})
	}
}

// TestMatchReportsEvalError covers an expression that compiles but fails
// at run time on a particular event. That drops the event rather than
// leaving it unmatched, and Failure must return the rule behind it.
func TestMatchReportsEvalError(t *testing.T) {
	var (
		env   = newTestEnv(t)
		state = newEvalState(t, evalTimeout, fullEvent())
	)
	// An absent key errors in CEL rather than reading as empty.
	got, reason := state.Match(compileSet(t, env, "event.labels['nope'] == 'x'"))
	if got || reason != ReasonEvalError {
		t.Fatalf("got (%t, %q), want (false, %q)", got, reason, ReasonEvalError)
	}
	assertFailureIsReported(t, state)
}

// TestMatchReportsCostOverrun covers the per-expression cost limit. It is
// a tenth of the per-event total, so an expensive expression trips its own
// limit first and Failure returns that expression rather than whichever
// one the event happened to run out on.
func TestMatchReportsCostOverrun(t *testing.T) {
	var (
		env   = newTestEnv(t)
		state = newEvalState(t, 10*time.Second, eventWithManyLabels(16<<10))
	)
	// A comprehension scanning a hundred sixteen kilobyte values,
	// which no single expression is allowed to finish.
	got, reason := state.Match(compileSet(t, env, "event.labels.all(k, v, !v.contains('zz'))"))
	if got || reason != ReasonEvalCost {
		t.Fatalf("got (%t, %q), want (false, %q)", got, reason, ReasonEvalCost)
	}
	assertFailureIsReported(t, state)
}

// TestEventCostSpansSets covers the per-event total cost, which the cel-go
// package cannot enforce itself. Its limit is fixed when a program is built,
// so nothing below can see what an event has already spent elsewhere.
func TestEventCostSpansSets(t *testing.T) {
	var (
		env   = newTestEnv(t)
		state = newEvalState(t, 10*time.Second, eventWithManyLabels(4<<10))
	)
	// One set costs well under the per-expression limit, so
	// the total is only reachable by carrying the cost from
	// one call to the next.
	set := compileSet(t, env, "event.labels.all(k, v, !v.contains('zz'))")

	for calls := 1; calls <= 200; calls++ {
		if _, reason := state.Match(set); reason == ReasonEvalCost {
			t.Logf("total spent over %d sets, cost=%d", calls, state.cost)
			if state.cost < perEventCostLimit {
				t.Errorf("got cost %d, want at least %d", state.cost, perEventCostLimit)
			}
			return
		}
	}
	t.Fatalf("got no cost reason over 200 sets, cost=%d", state.cost)
}

func TestResetClearsCost(t *testing.T) {
	var (
		env   = newTestEnv(t)
		state = newEvalState(t, 10*time.Second, fullEvent())
		set   = compileSet(t, env, "event.type == 'Warning'")
	)
	for range 100 {
		state.Match(set)
	}
	if state.cost == 0 {
		t.Fatal("nothing was charged, so a reset has nothing to clear")
	}
	state.Reset(fullEvent())
	if state.cost != 0 {
		t.Errorf("got cost %d after a reset, want 0", state.cost)
	}
}

// TestMatchReportsTimeout covers the wall-clock backstop, which catches
// what the cost model underestimates. A regex over each value is charged
// by argument size like any string call, and runs far slower per unit
// than what was assumed.
func TestMatchReportsTimeout(t *testing.T) {
	var (
		env   = newTestEnv(t)
		state = newEvalState(t, 5*time.Millisecond, eventWithManyLabels(4<<10))
	)
	start := time.Now()
	got, reason := state.Match(compileSet(t, env, "event.labels.all(k, v, !v.matches('v+z'))"))
	elapsed := time.Since(start)

	if got || reason != ReasonEvalTimeout {
		t.Fatalf("got (%t, %q) after %v, want (false, %q)", got, reason, elapsed, ReasonEvalTimeout)
	}
	t.Logf("interrupted after %v, cost=%d", elapsed.Round(time.Millisecond), state.cost)

	assertFailureIsReported(t, state)
}

// TestDeadlineIsReusedAcrossEvents covers the reuse of one evaluation
// context across many events. Creating one per event would otherwise
// allocate a runtime timer per event. The reuse is bounded so that an
// event still gets most of the timeout it was allocated.
func TestDeadlineIsReusedAcrossEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			state = newEvalState(t, evalTimeout, fullEvent())
			ev    = fullEvent()
			seen  = map[context.Context]bool{}
		)
		for range 100_000 {
			state.Reset(ev)

			if _, ok := state.ctx.Deadline(); !ok {
				t.Fatal("got a context with no deadline")
			}
			seen[state.ctx] = true
		}
		if len(seen) != 1 {
			t.Errorf("got %d contexts over 100K events, want 1", len(seen))
		}
		time.Sleep(state.refresh)
		state.Reset(ev)
		deadline, _ := state.ctx.Deadline()

		// An event arriving at the end of the refresh interval
		// is the one left with the least of the timeout, and
		// the divisor is what holds that to nine tenths.
		if got, want := time.Until(deadline), evalTimeout*9/10; got != want {
			t.Errorf("got %v of the timeout left, want %v", got, want)
		}
	})
}

// TestContextRotationCancelsPredecessor checks that the replacement of
// the evaluation context also cancels the previous one. Without that,
// the old timer runs on until its own deadline passes, so up to ten are
// live at once and the reuse saves nothing.
func TestContextRotationCancelsPredecessor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			ev    = fullEvent()
			state = newEvalState(t, time.Hour, ev)
		)
		previous := state.ctx
		time.Sleep(2 * state.refresh)
		state.Reset(ev)

		if state.ctx == previous {
			t.Fatal("got the same context, want a new one")
		}
		if previous.Err() == nil {
			t.Error("got a live previous context, want it canceled")
		}
	})
}

func TestCloseReleasesTheDeadline(t *testing.T) {
	var (
		state = newEvalState(t, time.Hour, fullEvent())
		live  = state.ctx
	)
	state.Close()

	if live.Err() == nil {
		t.Error("got a live context after Close, want it canceled")
	}
	if state.ctx != nil {
		t.Error("got a non-nil context after Close, want none")
	}
}

// TestCompileRacesEvaluation runs expressions compilation against
// an environment with running programs, which is what happen during
// a configuration reload.
func TestCompileRacesEvaluation(t *testing.T) {
	var (
		env  = newTestEnv(t)
		set  = compileSet(t, env, "event.type == 'Warning' && event.regarding.kind == 'Pod'")
		stop = make(chan struct{})
		wg   sync.WaitGroup
	)
	for range 16 {
		wg.Go(func() {
			state := NewEvalState(time.Minute)
			defer state.Close()

			ev := fullEvent()
			for {
				select {
				case <-stop:
					return
				default:
				}
				state.Reset(ev)

				if got, reason := state.Match(set); !got || reason != "" {
					t.Errorf("got (%t, %q) while compiling, want a match", got, reason)
					return
				}
			}
		})
	}
	for range 200 {
		expr := "event.reason == 'Failed' && event.labels.exists(k, k != '')"
		if _, err := env.Compile(expr); err != nil {
			t.Errorf("failed to compile against a live environment: %v", err)
			break
		}
	}
	close(stop)
	wg.Wait()
}

func assertFailureIsReported(t *testing.T, state *EvalState) {
	t.Helper()

	expr, err := state.Failure()
	if expr == "" {
		t.Error("got no expression from Failure, want the one that failed")
	}
	if err == nil {
		t.Error("got no error from Failure, want the cause")
	}
}

func newEvalState(t *testing.T, timeout time.Duration, ev *event.Event) *EvalState {
	t.Helper()

	state := NewEvalState(timeout)
	t.Cleanup(state.Close)
	state.Reset(ev)

	return state
}

func compileSet(tb testing.TB, env *Env, exprs ...string) Set {
	tb.Helper()

	set := make(Set, len(exprs))
	for i, expr := range exprs {
		rule, err := env.Compile(expr)
		if err != nil {
			tb.Fatalf("expr %q failed to compile: %v", expr, err)
		}
		set[i] = rule
	}
	return set
}

// eventWithManyLabels returns an event carrying a hundred labels
// of the given value size, which is what makes a comprehension
// over them expensive.
func eventWithManyLabels(size int) *event.Event {
	labels := make(map[string]string, 100)
	for i := range 100 {
		labels[fmt.Sprintf("k%03d", i)] = strings.Repeat("v", size)
	}
	return &event.Event{
		Labels:    labels,
		Namespace: "team-a",
		Reason:    "Failed",
		Note:      "Error: ImagePullBackOff",
		Type:      "Warning",
		Regarding: corev1.ObjectReference{Kind: "Pod", Name: "web-0"},
	}
}
