package webhook

import (
	"encoding/json/jsontext"
	"flag"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	goldenFile = "testdata/cloudevents.golden"
)

var update = flag.Bool("update", false, "update the golden file")

func TestCloudEventsGolden(t *testing.T) {
	encoder := newCloudEventsEncoder(CloudEvents{
		Source: "kem-agent-test",
		Type:   "io.k8s.event",
	})
	got := indentJSON(t, encoder.AppendEvent(nil, testEvent()))

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
		t.Errorf("the frame differs from the golden file (-want +got):\n%s", diff)
	}
}

func indentJSON(t *testing.T, b []byte) string {
	t.Helper()

	value := jsontext.Value(b)
	if err := value.Indent(jsontext.WithIndent("    ")); err != nil {
		t.Fatalf("failed to indent: %v", err)
	}
	return string(value)
}
