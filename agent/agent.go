package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/kubernetes"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/config"
	"github.com/shibernetes/kem-agent/filter"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/internal/kube"
	"github.com/shibernetes/kem-agent/pipeline"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/source"
)

// An Agent reads Kubernetes events and delivers them to the sinks its
// pipelines name.
//
// New builds every component and Run starts them. Building performs no
// I/O and starts nothing, so a configuration can be proved to run without
// opening a connection to a cluster or a sink destination.
type Agent struct {
	cfg          *config.Config
	logger       *slog.Logger
	registry     *prometheus.Registry
	metrics      *instruments
	kube         *kube.Config
	client       kubernetes.Interface
	factories    sink.Factories
	configPath   string
	lastConfig   []byte
	sinks        []*sinkEntry
	pipelines    []*pipeline.Pipeline
	fanouts      map[string]*pipeline.Fanout
	filters      atomic.Pointer[[]filter.Set]
	filterEnv    *filter.Env
	checkpointer *checkpoint.Checkpointer
	log          *slog.Logger
	server       *httpServer
	pprof        *httpServer
	enricher     source.Enricher
	source       *source.Source
	stopWatches  context.CancelFunc
	sourceDone   chan struct{}
	errs         chan error
	reload       chan struct{}
	reopen       chan struct{}
	ready        atomic.Bool
}

// Options configures an agent.
type Options struct {
	Factories  sink.Factories
	Logger     *slog.Logger
	ConfigPath string
	Kube       *kube.Config
}

// New builds every component the configuration declares.
func New(cfg *config.Config, opts Options) (*Agent, error) {
	if opts.Logger == nil {
		return nil, errors.New("agent: a logger is required")
	}
	if opts.Kube == nil {
		return nil, errors.New("agent: a Kubernetes client configuration is required")
	}
	registry := newRegistry()

	client, err := opts.Kube.ClientSet()
	if err != nil {
		return nil, err
	}
	metrics := newInstruments(registry)

	sinks, err := buildSinks(
		cfg.Sinks,
		opts.Factories,
		identity.Resolve(cfg.Service.ClusterName),
		metrics,
		opts.Logger,
	)
	if err != nil {
		return nil, err
	}
	pipelines, err := buildPipelines(cfg.Pipelines, sinks, metrics)
	if err != nil {
		return nil, err
	}
	env, err := filter.NewEnv()
	if err != nil {
		return nil, err
	}
	compiledFilters, err := compileFilters(env, cfg.Pipelines)
	if err != nil {
		return nil, err
	}
	store, err := buildStore(client, &cfg.Checkpoint.Store)
	if err != nil {
		return nil, err
	}
	a := &Agent{
		cfg:        cfg,
		logger:     opts.Logger,
		registry:   registry,
		metrics:    metrics,
		kube:       opts.Kube,
		client:     client,
		factories:  opts.Factories,
		configPath: opts.ConfigPath,
		sinks:      sinks,
		pipelines:  pipelines,
		filterEnv:  env,
		log:        opts.Logger.With(slog.String("component", "agent")),
		reload:     make(chan struct{}, 1),
		reopen:     make(chan struct{}, 1),
	}
	// The filters must be set before building the fan-outs below.
	// They keep a pointer to it and read it for every event.
	a.filters.Store(new(orderFilterSets(pipelines, compiledFilters, nil)))

	a.fanouts = pipeline.NewFanouts(pipeline.FanoutOptions{
		Pipelines: pipelines,
		Watches:   watchKeys(cfg.Source.Watches),
		Filters:   &a.filters,
		Timeout:   time.Duration(cfg.Service.CELEvalTimeout),
		Logger:    opts.Logger.With(slog.String("component", "pipeline")),
	})
	a.checkpointer = checkpoint.NewCheckpointer(
		store,
		reporterFunc(a.positions),
		cfg.Checkpoint.Config,
		metrics.forCheckpoint(),
		opts.Logger.With(slog.String("component", "checkpoint")),
	)
	registry.MustRegister(newQueueCollector(sinks))

	return a, nil
}

// Close releases every component of the agent.
func (a *Agent) Close(ctx context.Context) error {
	for _, f := range a.fanouts {
		f.Close()
	}
	var errs []error

	for _, entry := range a.sinks {
		if err := entry.sink.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("sinks[%s]: %w", entry.name, err))
		}
	}
	return errors.Join(errs...)
}

// positions reports how far each watch has been read.
func (a *Agent) positions() map[string]string {
	// The source is built once the resume state has been read,
	// which is after the checkpointer it reports to, so it is
	// nil until the watches open.
	if a.source == nil {
		return nil
	}
	return a.source.Positions()
}

// reporterFunc adapts a function to the [checkpoint.Reporter] interface.
type reporterFunc func() map[string]string

// Positions implements the [checkpoint.Reporter] interface.
func (f reporterFunc) Positions() map[string]string {
	return f()
}
