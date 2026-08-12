package checkpoint

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the instruments a checkpointer records into, which
// track whether the checkpoint is advancing. A skipped write means
// there was nothing to write, not that it failed, and distinguishes
// a quiet checkpointer from a stopped one.
type Metrics struct {
	Success prometheus.Counter // the state was written
	Failure prometheus.Counter // the store refused it
	Skipped prometheus.Counter // the state was unchanged
}
