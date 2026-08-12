package filter

import (
	"slices"
	"testing"
)

func TestFieldsReportsWhatAnExpressionReads(t *testing.T) {
	cases := map[string]struct {
		expr string
		want []string
	}{
		"one field":         {expr: "event.type == 'Warning'", want: []string{"type"}},
		"nested path":       {expr: "event.regarding.kind == 'Pod'", want: []string{"regarding.kind"}},
		"two paths":         {expr: "event.regarding.kind == event.related.kind", want: []string{"regarding.kind", "related.kind"}},
		"repeated path":     {expr: "event.type == 'Warning' || event.type == 'Normal'", want: []string{"type"}},
		"map addressed":     {expr: "event.labels['team'] == 'a'", want: []string{"labels"}},
		"comprehension":     {expr: "event.labels.exists(k, k == 'team')", want: []string{"labels"}},
		"nothing read":      {expr: "true", want: nil},
		"literal lookalike": {expr: "event.note.contains('regardingObject')", want: []string{"note"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := compileFields(t, tc.expr); !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A path another one extends is left out, so an expression guarding a
// read with a presence test reports the read alone.
func TestFieldsKeepsTheLongestPath(t *testing.T) {
	const expr = "has(event.regardingObject) && event.regardingObject.owner.kind == 'Deployment'"

	fields := compileFields(t, expr)
	if !slices.Equal(fields, []string{"regardingObject.owner.kind"}) {
		t.Errorf("got %q, want the extended path alone", fields)
	}
}

func TestFieldsRejectsUncompilableExpression(t *testing.T) {
	env, err := NewEnv()
	if err != nil {
		t.Fatalf("failed to create env: %v", err)
	}
	if _, err := env.Fields("event.nope == 1"); err == nil {
		t.Error("the expression was accepted, want it rejected")
	}
}

func compileFields(t *testing.T, expr string) []string {
	t.Helper()

	env, err := NewEnv()
	if err != nil {
		t.Fatalf("failed to create env %v", err)
	}
	fields, err := env.Fields(expr)
	if err != nil {
		t.Fatalf("failed to list fields for expression %q: %v", expr, err)
	}
	return fields
}
