package source

import (
	"github.com/prometheus/client_golang/prometheus"
)

// MetricsVec holds the instruments every watch resolves its children from.
type MetricsVec struct {
	Read     *prometheus.CounterVec // events read for delivery
	Restarts *prometheus.CounterVec // watch restarts, by reason
}

// watchMetrics holds the instruments one watch records into.
type watchMetrics struct {
	read           prometheus.Counter // an event was read for delivery
	restartClosed  prometheus.Counter // the APIServer closed a healthy watch
	restartError   prometheus.Counter // the watch failed and retried
	restartExpired prometheus.Counter // the resume position expired
}

// forWatch resolves the instruments of the watch over a namespace, which
// carries the name it is written under rather than the empty namespace a
// watch over every namespace is keyed by.
func (v MetricsVec) forWatch(namespace string) watchMetrics {
	return watchMetrics{
		read:           v.Read.WithLabelValues(namespace),
		restartClosed:  v.Restarts.WithLabelValues(namespace, "closed"),
		restartError:   v.Restarts.WithLabelValues(namespace, "error"),
		restartExpired: v.Restarts.WithLabelValues(namespace, "expired"),
	}
}
