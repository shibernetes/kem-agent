package agent

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

func TestMemoryStatsHandlerReportsMemoryInUse(t *testing.T) {
	report := memoryReport(t)

	keys := []string{
		"sysBytes", "heapAllocBytes", "heapInuseBytes", "heapObjects",
		"stackInuseBytes", "nextGCBytes", "goroutines",
	}
	for _, key := range keys {
		if v, _ := report[key].(float64); v <= 0 {
			t.Errorf("got %s=%v, want a positive value", key, report[key])
		}
	}
	// A process that has not collected yet can report zero for each of these.
	for _, key := range []string{"heapIdleBytes", "gcCycles", "gcCPUFraction"} {
		if _, ok := report[key]; !ok {
			t.Errorf("response has no key %s", key)
		}
	}
}

func TestMemoryStatsHandlerReportsLastGCTime(t *testing.T) {
	start := time.Now()
	runtime.GC()

	s, _ := memoryReport(t)["lastGC"].(string)
	got, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("got lastGC %q, want an RFC 3339 time: %v", s, err)
	}
	if got.Before(start) {
		t.Errorf("got lastGC %v, want the collection run by the test, at %v or later", got, start)
	}
}

func memoryReport(t *testing.T) map[string]any {
	t.Helper()

	rec := httptest.NewRecorder()
	memoryStatsHandler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/debug/memory", nil))

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("got content type %q, want %q", got, "application/json")
	}
	var report map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	return report
}
