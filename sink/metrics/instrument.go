package metrics

import (
	"maps"
	"slices"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/shibernetes/kem-agent/event"
)

// An instrument is one configured metric, registered as a counter and
// incremented once per event it records.
//
// It holds no mutable state, since every dispatch goroutine feeding the
// sink records through the same instruments.
type instrument struct {
	vec    *prometheus.CounterVec
	name   string
	fields []string
}

// newInstrument returns the instrument a metric declares, with its
// labels ordered by name.
func newInstrument(c Config, m Metric) *instrument {
	var (
		labels = slices.Sorted(maps.Keys(m.Labels))
		fields = make([]string, len(labels))
	)
	for i, label := range labels {
		fields[i] = m.Labels[label]
	}
	return &instrument{
		vec: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   c.namespace(m),
			Subsystem:   m.Subsystem,
			Name:        m.Name,
			Help:        m.Help,
			ConstLabels: prometheus.Labels(m.ConstLabels),
		}, labels),
		name:   c.fqName(m),
		fields: fields,
	}
}

// inc increments the counter for one combination of label values.
func (i *instrument) inc(values []string) {
	i.vec.WithLabelValues(values...).Inc()
}

// resolve appends the event's label values to dst, in the order the counter
// declares its labels. A field the event does not set resolves to an empty
// string, which is the label value Prometheus reads as absent.
func (i *instrument) resolve(dst []string, ev *event.Event) []string {
	for _, field := range i.fields {
		value, _ := ev.Field(field)
		dst = append(dst, value)
	}
	return dst
}
