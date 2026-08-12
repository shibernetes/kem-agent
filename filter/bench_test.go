package filter

import (
	"testing"
	"time"
)

// BenchmarkEvalCost reports what an expression costs in cel-go's units beside
// what it costs in wall clock. The two are not proportional, a cheap expression
// is dominated by the fixed cost of one evaluation, where an expensive one is
// dominated by the work it does. The rate between them therefore holds only over
// a range, and is worth measuring again whenever the environment, the enabled
// libraries or cel-go's cost model change.
func BenchmarkEvalCost(b *testing.B) {
	cases := map[string]struct {
		expr   string
		labels int
	}{
		"equality":            {expr: "event.type == 'Warning'"},
		"substring":           {expr: "event.note.contains('OOM')"},
		"reference field":     {expr: "event.regarding.kind == 'Pod'"},
		"five predicates":     {expr: "event.type == 'Warning' && event.regarding.kind == 'Pod' && event.reason != 'Scheduled' && event.namespace != 'kube-system' && event.note.contains('OOM')"},
		"small comprehension": {expr: "event.labels.all(k, v, !v.contains('zz'))", labels: 64},
		"large comprehension": {expr: "event.labels.all(k, v, !v.contains('zz'))", labels: 4 << 10},
	}
	for name, tc := range cases {
		b.Run(name, func(b *testing.B) {
			var (
				env   = newTestEnv(b)
				set   = compileSet(b, env, tc.expr)
				ev    = eventWithManyLabels(max(tc.labels, 8))
				state = NewEvalState(time.Minute)
			)
			b.Cleanup(state.Close)

			// One evaluation ahead of the timer reports the cost, which
			// covers a whole event and is cleared by every Reset.
			state.Reset(ev)
			state.Match(set)
			cost := state.cost

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				state.Reset(ev)
				state.Match(set)
			}
			b.StopTimer()
			b.ReportMetric(float64(cost), "units/op")
			b.ReportMetric(unitsPerMilli(cost, b.Elapsed(), b.N), "units/ms")
		})
	}
}

// unitsPerMilli returns the rate one evaluation charged its cost at.
func unitsPerMilli(cost uint64, elapsed time.Duration, runs int) float64 {
	if runs == 0 || elapsed == 0 {
		return 0
	}
	ns := float64(elapsed.Nanoseconds()) / float64(runs)
	return float64(cost) / ns * 1e6
}
