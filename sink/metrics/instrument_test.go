package metrics

import (
	"slices"
	"testing"
	"unsafe"

	"github.com/google/go-cmp/cmp"
)

// TestNewInstrumentSortsLabels asserts that the label values an event
// resolves to are ordered by label name, which is the order the counter
// declares them in.
func TestNewInstrumentSortsLabels(t *testing.T) {
	in := newInstrument(testConfig(nil), testMetric())

	// The metric declares reason before ns.
	want := []string{"namespace", "reason"}
	if !slices.Equal(in.fields, want) {
		t.Errorf("got fields %v, want %v", in.fields, want)
	}
}

func TestNewInstrumentName(t *testing.T) {
	cases := map[string]struct {
		namespace string
		subsystem string
		want      string
	}{
		"sink default": {"", "", "kem_events_warnings"},
		"override":     {"tenant", "", "tenant_warnings"},
		"subsystem":    {"", "pods", "kem_events_pods_warnings"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := testMetric()
			m.Namespace, m.Subsystem = tc.namespace, tc.subsystem

			if got := newInstrument(testConfig(nil), m).name; got != tc.want {
				t.Errorf("got name %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInstrumentResolve(t *testing.T) {
	cases := map[string]struct {
		labels map[string]string
		want   []string
	}{
		"in label order": {map[string]string{"reason": "reason", "ns": "namespace"}, []string{"team-a", "OOMKilled"}},
		"nested field":   {map[string]string{"kind": "regarding.kind"}, []string{"Pod"}},
		"unset field":    {map[string]string{"app": "labels.app"}, []string{""}},
		"no labels":      {nil, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := testMetric()
			m.Labels = tc.labels

			got := newInstrument(testConfig(nil), m).resolve(nil, testEvent())
			if !slices.Equal(got, tc.want) {
				t.Errorf("got values %v, want %v", got, tc.want)
			}
		})
	}
}

// TestInstrumentResolveReusesDestination asserts that the values are
// appended to the buffer the caller owns, which is what lets one buffer
// serve every instrument of an event.
func TestInstrumentResolveReusesDestination(t *testing.T) {
	in := newInstrument(testConfig(nil), testMetric())

	dst := make([]string, 0, 8)
	got := in.resolve(dst, testEvent())

	if len(got) != len(in.fields) {
		t.Fatalf("got %d values, want %d", len(got), len(in.fields))
	}
	if unsafe.SliceData(got) != unsafe.SliceData(dst) {
		t.Error("got a new slice, want the values appended to the buffer")
	}
}

func TestInstrumentInc(t *testing.T) {
	s := newTestSink(t, testConfig(nil))

	in := s.instruments[0]
	in.inc([]string{"team-a", "OOMKilled"})
	in.inc([]string{"team-a", "OOMKilled"})
	in.inc([]string{"team-b", "CrashLooping"})

	want := []string{
		`kem_events_warnings{ns="team-a",reason="OOMKilled"} 2`,
		`kem_events_warnings{ns="team-b",reason="CrashLooping"} 1`,
	}
	if diff := cmp.Diff(want, scrape(t, s)); diff != "" {
		t.Errorf("the series differ (-want +got):\n%s", diff)
	}
}

// TestInstrumentConstLabels asserts that a constant label is reported
// on every series, sorted with the rest of the resolved labels.
func TestInstrumentConstLabels(t *testing.T) {
	s := newTestSink(t, testConfig(func(c *Config) {
		c.Metrics[0].ConstLabels = map[string]string{"source": "kem-agent"}
	}))
	s.instruments[0].inc([]string{"team-a", "OOMKilled"})

	want := []string{`kem_events_warnings{ns="team-a",reason="OOMKilled",source="kem-agent"} 1`}
	if diff := cmp.Diff(want, scrape(t, s)); diff != "" {
		t.Errorf("the series differ (-want +got):\n%s", diff)
	}
}

func TestInstrumentWithoutLabels(t *testing.T) {
	s := newTestSink(t, testConfig(func(c *Config) {
		c.Metrics[0].Labels = nil
	}))
	s.instruments[0].inc(nil)

	want := []string{"kem_events_warnings 1"}
	if diff := cmp.Diff(want, scrape(t, s)); diff != "" {
		t.Errorf("the series differ (-want +got):\n%s", diff)
	}
}
