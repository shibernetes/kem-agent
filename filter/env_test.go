package filter

import (
	"errors"
	"testing"
)

func TestCompileCoversLibraries(t *testing.T) {
	env := newTestEnv(t)

	// Compile one expression per library the environment enables,
	// so a library that stops being registered is caught.
	cases := map[string]string{
		"comparison":          "event.type == 'Warning'",
		"nested field":        "event.regarding.kind == 'Pod'",
		"presence test":       "has(event.series)",
		"map index":           "event.labels['app.kubernetes.io/name'] == 'web'",
		"conjunction":         "event.type == 'Warning' && event.reason == 'Failed'",
		"comprehension macro": "event.labels.exists(k, k == 'team')",
		"optional types":      "event.?labels['team'].orValue('') == 'web'",
		"strings":             "event.note.lowerAscii().startsWith('pull')",
		"strings at 5":        "'%d'.format([1]) == '1'",
		"sets":                "sets.contains(['a', 'b'], ['a'])",
		"two-var macro":       "event.labels.all(k, v, k != '' && v != '')",
		"bindings":            "cel.bind(t, event.type, t == 'Warning')",
		"semver":              "semver('1.2.3').major() == 1",
		"regex":               "event.note.find('[0-9]+') != ''",
		"ip":                  "ip('10.0.0.1').family() == 4",
		"cidr":                "cidr('10.0.0.0/24').containsIP(ip('10.0.0.5'))",
	}
	for name, expr := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := env.Compile(expr); err != nil {
				t.Errorf("%q should compile: %v", expr, err)
			}
		})
	}
}

// TestCompileMarksInterruptibleRules asserts which expressions a deadline
// can stop. cel-go checks for interruption while folding a comprehension
// and nowhere else, so an expression holding none is evaluated without a
// context rather than paying for one it cannot use.
func TestCompileMarksInterruptibleRules(t *testing.T) {
	env := newTestEnv(t)

	cases := map[string]struct {
		expr string
		want bool
	}{
		"comparison":           {expr: "event.type == 'Warning'"},
		"conjunction":          {expr: "event.type == 'Warning' && event.reason == 'Failed'"},
		"map index":            {expr: "event.labels['team'] == 'web'"},
		"string call":          {expr: "event.note.lowerAscii().startsWith('pull')"},
		"sets call":            {expr: "sets.contains(['a', 'b'], ['a'])"},
		"comprehension":        {expr: "event.labels.all(k, v, v != '')", want: true},
		"comprehension nested": {expr: "event.type == 'Warning' && event.labels.exists(k, k == 'team')", want: true},
		"bind macro":           {expr: "cel.bind(t, event.type, t == 'Warning')", want: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rule, err := env.Compile(tc.expr)
			if err != nil {
				t.Fatalf("%q should compile: %v", tc.expr, err)
			}
			switch {
			case tc.want && !rule.interruptible:
				t.Errorf("%q is evaluated without a deadline, want it interruptible", tc.expr)
			case !tc.want && rule.interruptible:
				t.Errorf("%q is evaluated under a deadline it cannot be stopped by", tc.expr)
			}
		})
	}
}

func TestCompileRejectsBadExpressions(t *testing.T) {
	env := newTestEnv(t)

	cases := map[string]string{
		"syntax error":     "event.type ==",
		"unknown field":    "event.unknown == 'x'",
		"unknown variable": "foo == 1",
		"type mismatch":    "event.type == 5",
		"non-bool result":  "event.note",
		"nested unknown":   "event.regarding.unknown == 'x'",
	}
	for name, expr := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := env.Compile(expr); err == nil {
				t.Errorf("%q should be rejected, it compiled", expr)
			}
		})
	}
}

func TestCompileIssueHasPosition(t *testing.T) {
	issues := compileIssues(t, newTestEnv(t), "event.unknown == 'x'")
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].Line != 1 || issues[0].Column != 6 {
		t.Errorf("got position %d:%d, want 1:6", issues[0].Line, issues[0].Column)
	}
}

func TestCompileIssueSpansSymbol(t *testing.T) {
	cases := map[string]struct {
		expr   string
		column int
		length int
	}{
		"field":        {expr: "event.unknown == 'Warning'", column: 6, length: 7},
		"nested field": {expr: "event.regarding.knd == 'Pod'", column: 16, length: 3},
		"identifier":   {expr: "nope.unknown == 'x'", column: 0, length: 4},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			issues := compileIssues(t, newTestEnv(t), tc.expr)
			if len(issues) != 1 {
				t.Fatalf("got %d issues, want 1", len(issues))
			}
			if issues[0].Column != tc.column || issues[0].Length != tc.length {
				t.Errorf("got column %d spanning %d chars, want %d spanning %d",
					issues[0].Column, issues[0].Length, tc.column, tc.length)
			}
		})
	}
}

func TestCompileIssueSpansNothingWithoutSymbol(t *testing.T) {
	issues := compileIssues(t, newTestEnv(t), "event.reason.contains(3)")
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	// A position that doesn't point at a CEL identifier must keep the
	// column cel-go reported, and spans nothing, rather than covering
	// an unrelated symbol.
	if issues[0].Column != 21 || issues[0].Length != 0 {
		t.Errorf("got column %d spanning %d chars, want 21 spanning 0",
			issues[0].Column, issues[0].Length)
	}
}

func TestCompileIssueSpansPastMultiByteRune(t *testing.T) {
	issues := compileIssues(t, newTestEnv(t), "event.note == 'café' && event.unknown == 'x'")

	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	// cel-go counts a column index in runes, so a multi-byte character
	// earlier in the expression must not shift the span off the symbol.
	if issues[0].Column != 30 || issues[0].Length != 7 {
		t.Errorf("got column %d spanning %d chars, want 30 spanning 7",
			issues[0].Column, issues[0].Length)
	}
}

func TestCompileIssueSpansSymbolOnLaterLine(t *testing.T) {
	issues := compileIssues(t, newTestEnv(t),
		"event.type == 'Warning' &&\nevent.unknown == 'x'",
	)
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	iss := issues[0]
	if iss.Line != 2 || iss.Column != 6 || iss.Length != 7 {
		t.Errorf("got line %d column %d spanning %d chars, want line 2 column 6 spanning 7",
			iss.Line, iss.Column, iss.Length)
	}
}

func TestCompileErrorSourceLine(t *testing.T) {
	ce := &CompileError{
		Expr: "event.type == 'Warning' &&\nevent.unknown == 'x'\n",
	}
	cases := map[string]struct {
		n    int
		want string
	}{
		"first line":         {n: 1, want: "event.type == 'Warning' &&"},
		"last line":          {n: 2, want: "event.unknown == 'x'"},
		"no position":        {n: 0, want: "event.type == 'Warning' &&"},
		"past the last line": {n: 9, want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ce.SourceLine(tc.n); got != tc.want {
				t.Errorf("line %d is %q, want %q", tc.n, got, tc.want)
			}
		})
	}
}

func TestCompileIssueWithoutAPosition(t *testing.T) {
	issues := compileIssues(t, newTestEnv(t), "event.note")
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].Line != 0 {
		t.Errorf("got line %d, want an unpositioned issue", issues[0].Line)
	}
}

func newTestEnv(tb testing.TB) *Env {
	tb.Helper()

	env, err := NewEnv()
	if err != nil {
		tb.Fatalf("failed to build environment: %v", err)
	}
	return env
}

func compileIssues(t *testing.T, env *Env, expr string) []CompileIssue {
	t.Helper()

	_, err := env.Compile(expr)
	if err == nil {
		t.Fatalf("%q should be rejected, but it compiled", expr)
	}
	compileErr, ok := errors.AsType[*CompileError](err)
	if !ok {
		t.Fatalf("%q was rejected with a %T, want a *CompileError", expr, err)
	}
	return compileErr.Issues
}
