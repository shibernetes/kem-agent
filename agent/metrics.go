package agent

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/internal/version"
	"github.com/shibernetes/kem-agent/pipeline"
	"github.com/shibernetes/kem-agent/source"
	"github.com/shibernetes/kem-agent/source/enricher"
)

const (
	metricNamespace = version.MetricNamespace
)

var sendDurationBuckets = []float64{
	.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30,
}

var _ prometheus.Collector = (*queueCollector)(nil)

type queueCollector struct {
	sinks []*sinkEntry
	usage *prometheus.Desc
	limit *prometheus.Desc
}

type instruments struct {
	read      *prometheus.CounterVec
	matched   *prometheus.CounterVec
	delivered *prometheus.CounterVec
	dropped   *prometheus.CounterVec
	sends     *prometheus.HistogramVec
	restarts  *prometheus.CounterVec
	saves     *prometheus.CounterVec
	reloads   *prometheus.CounterVec
	enriched  *prometheus.CounterVec
}

func newQueueCollector(sinks []*sinkEntry) *queueCollector {
	return &queueCollector{
		sinks: sinks,
		usage: prometheus.NewDesc(
			metricNamespace+"_queue_usage_bytes",
			"Number of bytes currently held in a sink's queue.",
			[]string{"sink"}, nil,
		),
		limit: prometheus.NewDesc(
			metricNamespace+"_queue_limit_bytes",
			"Maximum number of bytes a sink's queue holds before dropping the oldest.",
			[]string{"sink"}, nil,
		),
	}
}

// newInstruments declares the agent's own metrics and registers them.
func newInstruments(reg prometheus.Registerer) *instruments {
	reg.MustRegister(buildInfo())

	return &instruments{
		sends:     sendDuration(reg),
		read:      counter(reg, "events_read_total", "Total number of events read for delivery.", "namespace"),
		matched:   counter(reg, "events_matched_total", "Total number of events accepted by a pipeline's filters, counted once per matching pipeline.", "pipeline"),
		delivered: counter(reg, "events_delivered_total", "Total number of events delivered by a sink, less any record its destination refused.", "sink"),
		dropped:   counter(reg, "events_dropped_total", "Total number of events dropped, by stage and reason.", "stage", "reason", "pipeline", "sink"),
		restarts:  counter(reg, "watch_restarts_total", "Total number of watch restarts, by reason.", "namespace", "reason"),
		saves:     counter(reg, "checkpoint_saves_total", "Total number of checkpoint writes, by result.", "result"),
		reloads:   counter(reg, "config_hot_reloads_total", "Total number of configuration reloads, by result.", "result"),
		enriched:  counter(reg, "events_enriched_total", "Total number of enrichment lookups, by result.", "result"),
	}
}

// forBatchSink resolves what the delivery path of one batch sink records into.
// The send histogram is curried rather than resolved, because its remaining
// label is the outcome of an attempt that has not happened yet.
func (in *instruments) forBatchSink(name string) pipeline.Metrics {
	return pipeline.Metrics{
		Delivered:      in.delivered.WithLabelValues(name),
		Overflow:       in.dropped.WithLabelValues("queue", "overflow", "", name),
		TooLarge:       in.dropped.WithLabelValues("queue", "too_large", "", name),
		RetryExhausted: in.dropped.WithLabelValues("delivery", "retry_exhausted", "", name),
		PermanentError: in.dropped.WithLabelValues("delivery", "permanent_error", "", name),
		Rejected:       in.dropped.WithLabelValues("sink", "rejected", "", name),
		SendDuration:   in.sends.MustCurryWith(prometheus.Labels{"sink": name}),
	}
}

// forEventSink resolves what a sink consuming events directly records into.
func (in *instruments) forEventSink(name string) pipeline.Metrics {
	return pipeline.Metrics{
		Delivered: in.delivered.WithLabelValues(name),
		Rejected:  in.dropped.WithLabelValues("sink", "rejected", "", name),
	}
}

// forPipeline resolves what one pipeline's filters record into.
func (in *instruments) forPipeline(name string) pipeline.FilterMetrics {
	return pipeline.FilterMetrics{
		Matched:     in.matched.WithLabelValues(name),
		EvalError:   in.dropped.WithLabelValues("filter", "eval_error", name, ""),
		EvalCost:    in.dropped.WithLabelValues("filter", "eval_cost", name, ""),
		EvalTimeout: in.dropped.WithLabelValues("filter", "eval_timeout", name, ""),
	}
}

// forSource returns the vectors the source records into, since it knows
// its watches and resolves a counter for each.
func (in *instruments) forSource() source.MetricsVec {
	return source.MetricsVec{
		Read:     in.read,
		Restarts: in.restarts,
	}
}

// forCheckpoint resolves what the checkpointer records every write into.
func (in *instruments) forCheckpoint() checkpoint.Metrics {
	return checkpoint.Metrics{
		Success: in.saves.WithLabelValues("success"),
		Failure: in.saves.WithLabelValues("failure"),
		Skipped: in.saves.WithLabelValues("skipped"),
	}
}

// forEnricher resolves what the enrichment cache records every lookup into.
func (in *instruments) forEnricher() enricher.Metrics {
	return enricher.Metrics{
		Hit:            in.enriched.WithLabelValues("hit"),
		Miss:           in.enriched.WithLabelValues("miss"),
		Undeclared:     in.enriched.WithLabelValues("undeclared"),
		CrossNamespace: in.enriched.WithLabelValues("cross_namespace"),
		Skipped:        in.enriched.WithLabelValues("skipped"),
	}
}

// Describe implements the [prometheus.Collector] interface.
func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.usage
	ch <- c.limit
}

// Collect implements the [prometheus.Collector] interface.
func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	for _, entry := range c.sinks {
		// A sink consuming events directly has no queue.
		if entry.drainer == nil {
			continue
		}
		ch <- prometheus.MustNewConstMetric(
			c.usage, prometheus.GaugeValue, float64(entry.drainer.Usage()), entry.name,
		)
		ch <- prometheus.MustNewConstMetric(
			c.limit, prometheus.GaugeValue, float64(entry.queueLimit), entry.name,
		)
	}
}

func newRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()

	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

func counter(reg prometheus.Registerer, name, help string, labels ...string) *prometheus.CounterVec {
	vec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace,
		Name:      name,
		Help:      help,
	}, labels)
	reg.MustRegister(vec)

	return vec
}

func sendDuration(reg prometheus.Registerer) *prometheus.HistogramVec {
	vec := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricNamespace,
		Name:      "send_duration_seconds",
		Help:      "Duration of one delivery attempt in seconds, by outcome.",
		Buckets:   sendDurationBuckets,
	}, []string{
		"sink",
		"result",
	})
	reg.MustRegister(vec)

	return vec
}

func buildInfo() prometheus.Collector {
	info := version.Get()

	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricNamespace,
		Name:      "build_info",
		Help:      "Information about the agent build.",
		ConstLabels: prometheus.Labels{
			"version":    info.Semver(),
			"commit":     info.Commit,
			"go_version": info.GoVersion,
		},
	})
	gauge.Set(1)

	return gauge
}
