package agent

import (
	"testing"
)

func TestEnrichmentWarningsReportEveryReader(t *testing.T) {
	want := []string{
		"pipelines[owned]: filters[0] reads regardingObject.owner.kind",
		"sinks[count]: metrics[0].labels.team reads regardingObject.labels.team",
	}

	got := warningMessages(t, "reads-enrichment")

	// The enriched object is nil when the enrichment config declares
	// no resource, so everything reading it is reported.
	if len(got) != len(want) {
		t.Fatalf("got %d warnings, want %d: %q", len(got), len(want), got)
	}
	for i, message := range want {
		if got[i][:len(message)] != message {
			t.Errorf("got message %q, want it to start with %q", got[i], message)
		}
	}
}

func TestEnrichmentWarningsAreSilentOnceDeclared(t *testing.T) {
	// A declared resource fills the object in, so nothing is reported.
	if got := warningMessages(t, "enrichment-declared"); len(got) != 0 {
		t.Errorf("got %q, want none", got)
	}
}

func TestEnrichmentWarningsIgnoreALiteral(t *testing.T) {
	// The AST walk reads the compiled expression, so a quoted
	// literal of the field name is ignored and not reported.
	if got := warningMessages(t, "literal-lookalike"); len(got) != 0 {
		t.Errorf("got %q, want none", got)
	}
}

func TestEnrichmentWarningsHasNode(t *testing.T) {
	parsed := parseFixture(t, "reads-enrichment")

	for _, warning := range EnrichmentWarnings(parsed) {
		if line, _ := warning.Position(); line == 0 {
			t.Errorf("got %q with no position, want it positioned", warning.Message)
		}
	}
}

func warningMessages(t *testing.T, fixture string) []string {
	t.Helper()

	warnings := EnrichmentWarnings(parseFixture(t, fixture))

	out := make([]string, len(warnings))
	for i, warning := range warnings {
		out[i] = warning.Message
	}
	return out
}
