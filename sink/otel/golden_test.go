package otel

import (
	"encoding/json/jsontext"
	"flag"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	goldenFile = "testdata/export.golden"
)

var update = flag.Bool("update", false, "update the golden file")

// TestComposedRequest asserts the export request a batch composes to,
// against a golden file that reads as the wire-format reference.
func TestComposedRequest(t *testing.T) {
	var (
		payload = composeBatch(newTestSink(t, DefaultConfig()), fullEvent(), testEvent())
		got     = goldenRequest(t, decodeBatch(t, payload))
	)
	if *update {
		if err := os.WriteFile(goldenFile, got, 0o644); err != nil {
			t.Fatalf("failed to write golden file: %v", err)
		}
	}
	want, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("failed to read golden file: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("the payload differs from the golden file (-want +got):\n%s", diff)
	}
}

// goldenRequest renders a logs service request as JSON.
func goldenRequest(t *testing.T, req *collogs.ExportLogsServiceRequest) []byte {
	t.Helper()

	for _, rlogs := range req.GetResourceLogs() {
		for _, slogs := range rlogs.GetScopeLogs() {
			for _, rec := range slogs.GetLogRecords() {
				// The observed time is the encoding time, so it is replaced by a fixed one.
				rec.ObservedTimeUnixNano = uint64(observedAt.UnixNano())
			}
		}
	}
	raw, err := protojson.Marshal(req)
	if err != nil {
		t.Fatalf("failed to encode request: %v", err)
	}
	b := jsontext.Value(raw)

	// protojson varies its spacing across builds, which indenting removes.
	if err := b.Indent(jsontext.WithIndent("  ")); err != nil {
		t.Fatalf("failed to indent output: %v", err)
	}
	return append(b, '\n')
}
