package pipeline

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the instruments a sink's delivery path records into.
// A sink consuming events directly has neither a queue nor a delivery
// stage, so it is given only Delivered and Rejected.
type Metrics struct {
	Delivered      prometheus.Counter     // events delivered
	Overflow       prometheus.Counter     // dropped, queue stage
	TooLarge       prometheus.Counter     // dropped, queue stage
	RetryExhausted prometheus.Counter     // dropped, delivery stage
	PermanentError prometheus.Counter     // dropped, delivery stage
	Rejected       prometheus.Counter     // dropped, sink stage
	SendDuration   prometheus.ObserverVec // one attempt, labeled by result
}

// FilterMetrics holds the instruments one pipeline's filters record into.
// An event the filters simply do not match is not counted. Only one whose
// evaluation failed is, because it is dropped.
type FilterMetrics struct {
	Matched     prometheus.Counter // events the filters accepted
	EvalError   prometheus.Counter // dropped, filter stage
	EvalCost    prometheus.Counter // dropped, filter stage
	EvalTimeout prometheus.Counter // dropped, filter stage
}
