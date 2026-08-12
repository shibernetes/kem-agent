package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/sink"
)

func TestParseRejectsDocument(t *testing.T) {
	cases := map[string]struct {
		doc  string
		want string
	}{
		"two documents":  {doc: "source: {}\n---\nsource: {}\n", want: "single YAML document"},
		"no document":    {doc: "", want: "empty"},
		"only comments":  {doc: "# nothing here\n", want: "empty"},
		"scalar root":    {doc: "kem-agent\n", want: "must be an object"},
		"duplicate keys": {doc: "source: {}\nsource: {}\n", want: "already defined"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.doc), sink.Factories{fakeType: fakeFactory{}})
			if err == nil {
				t.Fatalf("parsed %q, want a rejection", tc.doc)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestParseRejectsConfig covers the rules that reject a whole document.
// Every rejection must also name the line it was raised at, which is what
// the renderer points its caret at.
func TestParseRejectsConfig(t *testing.T) {
	cases := map[string]struct {
		fixture string
		want    string
	}{
		"misspelled sink key":        {fixture: "unknown-field", want: `unknown field "unknown"`},
		"unknown sink type":          {fixture: "unknown-sink-type", want: `unknown type "nowhere"`},
		"missing required key":       {fixture: "missing-endpoint", want: "endpoint is required"},
		"sink no pipeline names":     {fixture: "unreferenced-sink", want: "referenced by no pipeline"},
		"pipeline naming no sink":    {fixture: "undeclared-sink", want: `"elsewhere" is not declared`},
		"namespace not watched":      {fixture: "unwatched-namespace", want: `"team-b" is not watched`},
		"watch no pipeline reads":    {fixture: "unconsumed-watch", want: `"team-b" is referenced by no pipeline`},
		"narrowing every namespace":  {fixture: "narrowed-all-namespaces", want: "cannot be narrowed"},
		"one metric on two sinks":    {fixture: "duplicate-metric", want: "already exposed by sink"},
		"pprof address with no port": {fixture: "pprof-addr", want: "pprof_server: invalid addr"},
		"unselectable event field":   {fixture: "bad-field-selector", want: "not one of the selectable event fields"},
		"save interval of zero":      {fixture: "bad-save-interval", want: "save_interval must be positive"},
		"store name with a space":    {fixture: "bad-store-name", want: `invalid name "Kem Agent"`},
		"sink key with no value":     {fixture: "sink-no-value", want: "sinks[spare]: component is not declared"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := parseFixtureErr(t, tc.fixture)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want it to mention %q", err, tc.want)
			}
			if _, line, _ := Diagnose(err); line == 0 {
				t.Error("got no position, want it to report a line number")
			}
		})
	}
}

func TestParseReturnsWarningsBesideValidationError(t *testing.T) {
	res, err := Parse(readFixture(t, "warning-beside-error"), sink.Factories{fakeType: fakeFactory{}})
	if err == nil {
		t.Fatal("the configuration was accepted, want it rejected")
	}
	if res == nil {
		t.Fatal("got no result, want the warnings returned beside the error")
	}
	if len(res.Warnings) != 1 {
		t.Errorf("got %d warnings, want 1", len(res.Warnings))
	}
}

func TestParseReturnsNoResultWhenDecodingFails(t *testing.T) {
	res, err := Parse(readFixture(t, "unknown-field"), sink.Factories{fakeType: fakeFactory{}})
	if err == nil {
		t.Fatal("the configuration was accepted, want it rejected")
	}
	if res != nil {
		t.Error("got a result, want none for a config that does not decode")
	}
}

// TestParseAppliesDefaults checks that a sink block is decoded onto the
// config its factory returns, so an absent key keeps the factory's value.
func TestParseAppliesDefaults(t *testing.T) {
	cfg := parseFixture(t, "minimal").Config

	if got := fakeSinkConfig(t, cfg, "out").Method; got != defaultFakeMethod {
		t.Errorf("got method %q, want the default %q", got, defaultFakeMethod)
	}
}

func TestParseFailureFieldPath(t *testing.T) {
	cases := map[string]struct {
		fixture string
		want    string
	}{
		"watch selector":    {fixture: "bad-field-selector", want: "$.source.watches[0].field_selector"},
		"checkpoint tick":   {fixture: "bad-save-interval", want: "$.checkpoint.save_interval"},
		"store name":        {fixture: "bad-store-name", want: "$.checkpoint.store.name"},
		"pprof address":     {fixture: "pprof-addr", want: "$.service.pprof_server.addr"},
		"sink declaration":  {fixture: "sink-no-value", want: "$.sinks.spare"},
		"unreferenced sink": {fixture: "unreferenced-sink", want: "$.sinks.spare"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := parseFixtureErr(t, tc.fixture)

			var positioned *diag.Error
			if !errors.As(err, &positioned) || positioned.Node() == nil {
				t.Fatalf("got %v, want an error wrapping a node", err)
			}
			if got := positioned.Node().GetPath(); got != tc.want {
				t.Errorf("got path %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFilterLineFollowsScalarStyle(t *testing.T) {
	res := parseFixture(t, "filter-scalar-styles")

	// A literal block (|) keeps the line breaks of its expression,
	// so its lines map to the document one to one. A folded block
	// (>) or a quoted/plain string joins them into one line, reported
	// where its content starts.
	cases := map[string]struct {
		pipeline string
		n        int
		want     int
	}{
		"literal first line":  {pipeline: "literal", n: 1, want: 12},
		"literal last line":   {pipeline: "literal", n: 2, want: 13},
		"literal no position": {pipeline: "literal", n: 0, want: 12},
		"folded last line":    {pipeline: "folded", n: 2, want: 19},
		"quoted":              {pipeline: "quoted", n: 1, want: 25},
		"undeclared pipeline": {pipeline: "nowhere", n: 1, want: 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := res.FilterLine(tc.pipeline, 0, tc.n); got != tc.want {
				t.Errorf("line %d of %s is at document line %d, want %d", tc.n, tc.pipeline, got, tc.want)
			}
		})
	}
}

func TestDiagnoseReturnsPositions(t *testing.T) {
	cases := map[string]struct {
		fixture string
		message string
		line    int
		column  int
	}{
		"diag.Error":    {fixture: "bad-field-selector", message: "source: watches[0]: field_selector", line: 4, column: 23},
		"decoder error": {fixture: "unknown-field", message: `unknown field "unknown"`, line: 7, column: 5},
		"unset field":   {fixture: "missing-endpoint", message: "endpoint is required", line: 5, column: 3},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			message, line, column := Diagnose(parseFixtureErr(t, tc.fixture))

			if !strings.Contains(message, tc.message) {
				t.Errorf("got message %q, want it to contain %q", message, tc.message)
			}
			if strings.HasPrefix(message, "[") {
				t.Errorf("got message %q, want no position prefix", message)
			}
			if line != tc.line || column != tc.column {
				t.Errorf("got position %d:%d, want %d:%d", line, column, tc.line, tc.column)
			}
		})
	}
}

func TestDiagnoseIgnoresPlainError(t *testing.T) {
	message, line, column := Diagnose(errors.New("nothing positioned"))

	if message != "nothing positioned" {
		t.Errorf("got message %q, want it unchanged", message)
	}
	if line != 0 || column != 0 {
		t.Errorf("got position %d:%d, want none", line, column)
	}
}

func TestSpanCoversKeyNotValue(t *testing.T) {
	cases := map[string]struct {
		fixture string
		want    int
	}{
		"unknown key":    {fixture: "unknown-field", want: len("unknown")},
		"undeclared key": {fixture: "sink-no-value", want: len("spare")},
		"rejected value": {fixture: "bad-store-name", want: 0},
		"whole block":    {fixture: "bad-save-interval", want: 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Span(parseFixtureErr(t, tc.fixture)); got != tc.want {
				t.Errorf("got span %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSpanIsZeroForUnpositionedError(t *testing.T) {
	if got := Span(errors.New("nothing positioned")); got != 0 {
		t.Errorf("got span %d, want none", got)
	}
}
