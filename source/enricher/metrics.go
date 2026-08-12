package enricher

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the instruments a [Cache] records every lookup into.
type Metrics struct {
	Hit            prometheus.Counter // the object was cached
	Miss           prometheus.Counter // no matching object was cached
	Undeclared     prometheus.Counter // the resource has no informer
	CrossNamespace prometheus.Counter // outside the event's own namespace
	Skipped        prometheus.Counter // the reference names no object
}
