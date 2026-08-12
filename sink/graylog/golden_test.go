package graylog

import (
	"encoding/json/jsontext"
	"flag"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	goldenFile = "testdata/message.golden"
)

var update = flag.Bool("update", false, "update the golden file")

func TestGoldenEvent(t *testing.T) {
	cfg := testConfig()
	cfg.AdditionalFields = map[string]string{"env": "production"}

	got := indent(t, encodeWith(t, cfg, agentMetadata(), testEvent()))

	if *update {
		if err := os.WriteFile(goldenFile, []byte(got), 0o644); err != nil {
			t.Fatalf("failed to write golden file: %v", err)
		}
		t.SkipNow()
	}
	want, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("failed to read golden file: %v", err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("the message differs from the golden file (-want +got):\n%s", diff)
	}
}

func indent(t *testing.T, s string) string {
	t.Helper()

	value := jsontext.Value(s)
	if err := value.Indent(jsontext.WithIndent("    ")); err != nil {
		t.Fatalf("failed to indent: %v", err)
	}
	return string(value)
}
