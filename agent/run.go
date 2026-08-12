package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/kube"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/source"
	"github.com/shibernetes/kem-agent/source/enricher"
	"github.com/shibernetes/kem-agent/source/sanitizer"
)

const (
	// startupPhaseTimeout bounds each phase that runs before the watches
	// open. A signal cancels the context they share, and this deadline
	// ends one that hangs when no signal comes. Without it the agent would
	// stay up and respond to its probes while it reads nothing.
	startupPhaseTimeout = 30 * time.Second
)

// Run runs the agent and blocks until the context is done, a stop
// signal arrives, or a component reports a failure it cannot handle.
// It releases everything before it returns, whether it starts or not.
func (a *Agent) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)

	stopSignals := watchSignals(cancel, a.log)
	defer stopSignals()

	// SIGHUP has to be relayed before the startup phases, because a
	// process with no handler for it takes the default disposition and
	// dies. A request arriving now waits for the loop that drains it.
	go relaySIGHUP(ctx, a.onSIGHUP)

	// The shutdown sequence waits for the checkpointer, which runs
	// on this context, so cancelling first allows it to finish.
	defer func() {
		cancel()
		a.stop()
	}()

	if err := a.start(ctx); err != nil {
		return err
	}
	a.log.LogAttrs(ctx, slog.LevelInfo, "agent started")

	var err error
	select {
	case <-ctx.Done():
	case err = <-a.errs:
	}
	return err
}

// start starts the agent's components in order and returns once the watches
// are open. Every phase runs on the context the stop cancels, so a signal ends
// a slow one at once instead of waiting for it to finish.
func (a *Agent) start(ctx context.Context) error {
	if a.cfg.Service.HotReload && a.configPath == "" {
		return errors.New("agent: hot reload needs the config path")
	}
	a.errs = make(chan error, 3)
	a.sourceDone = make(chan struct{})

	if err := a.serve(); err != nil {
		return err
	}
	if err := a.checkServerVersion(ctx); err != nil {
		return err
	}
	state, err := a.readCheckpoint(ctx)
	if err != nil {
		return err
	}
	if err := a.startSinks(ctx); err != nil {
		return err
	}
	if err := a.startEnrichment(ctx); err != nil {
		return err
	}
	// Readiness latches once every phase that can kill the process has
	// passed, so a rollout waits until the agent can read events.
	a.ready.Store(true)

	if err := a.startSource(ctx, state); err != nil {
		return err
	}
	// The source is set before the checkpointer reads it.
	go a.checkpointer.Run(ctx)

	go a.reopenLoop(ctx)

	if a.cfg.Service.HotReload {
		go a.reloadLoop(ctx)
		return a.watchConfigFile(ctx)
	}
	return nil
}

// serve binds the local HTTP server, and the pprof server when an address
// is configured for it. They are started before any phase that can block,
// so the probes are up while a slow sync runs.
func (a *Agent) serve() error {
	var reload func()

	if a.cfg.Service.HotReload {
		reload = a.triggerReload
	}
	srv, err := newServer(
		a.cfg.Service.HTTPServer.Addr,
		&a.ready,
		reload,
		a.registry,
		a.logger,
	)
	if err != nil {
		return err
	}
	a.server = srv

	go func() {
		if err := srv.run(); err != nil {
			a.errs <- err
		}
	}()
	if a.cfg.Service.PprofServer.Addr == "" {
		return nil
	}
	pprofSrv, err := newPprofServer(a.cfg.Service.PprofServer.Addr, a.logger)
	if err != nil {
		return err
	}
	a.pprof = pprofSrv
	go func() {
		if err := pprofSrv.run(); err != nil {
			a.errs <- err
		}
	}()
	return nil
}

// checkServerVersion checks that the APIServer supports the streaming
// list options applied to every watch.
func (a *Agent) checkServerVersion(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, startupPhaseTimeout)
	defer cancel()

	serverVersion, err := kube.CheckServerVersion(ctx, a.client)
	if err != nil {
		return err
	}
	a.log.LogAttrs(ctx, slog.LevelInfo, "APIServer version supported",
		slog.String("version", serverVersion),
	)
	return nil
}

// readCheckpoint returns the state the watches resume from.
func (a *Agent) readCheckpoint(ctx context.Context) (checkpoint.State, error) {
	ctx, cancel := context.WithTimeout(ctx, startupPhaseTimeout)
	defer cancel()

	return a.checkpointer.Read(ctx)
}

// startSinks registers each sink's metrics, opens its destination and
// starts its drainer.
//
// A destination that fails to open is logged and the agent carries on,
// because one broken sink must not take the others down. A metric that
// fails to register stops the agent, since nothing at runtime can fix it.
func (a *Agent) startSinks(ctx context.Context) error {
	for _, entry := range a.sinks {
		if instrumented, ok := entry.sink.(sink.Instrumented); ok {
			for _, collector := range instrumented.Collectors() {
				if err := a.registry.Register(collector); err != nil {
					return fmt.Errorf("sinks[%s]: failed to register a metric: %w", entry.name, err)
				}
			}
		}
		if entry.opener != nil {
			if err := entry.opener.Open(ctx); err != nil {
				a.log.LogAttrs(ctx, slog.LevelError, "failed to open the sink destination",
					slog.String("sink", entry.name),
					slog.Any("error", err),
				)
			}
		}
		if entry.drainer != nil {
			go entry.drainer.Run()
			entry.started = true
		}
	}
	return nil
}

// startEnrichment fills the metadata caches of the declared resources.
// Nothing is built when none is declared, since resolving one reads
// discovery and enrichment is off by default.
func (a *Agent) startEnrichment(ctx context.Context) error {
	if len(a.cfg.Source.Enrichment.Resources) == 0 {
		a.enricher = nopEnricher{}
		return nil
	}
	client, err := a.kube.MetadataClient()
	if err != nil {
		return err
	}
	logger := a.logger.With(slog.String("component", "enrichment"))

	mapper, err := a.restMapper(ctx)
	if err != nil {
		return err
	}
	cache, err := enricher.New(ctx, a.cfg.Source.Enrichment, enricher.Options{
		Client:     client,
		Mapper:     mapper,
		Watches:    watchKeys(a.cfg.Source.Watches),
		Sanitizers: sanitizer.New(a.cfg.Source.Sanitizers).Metadata(),
		Metrics:    a.metrics.forEnricher(),
		Logger:     logger,
	})
	if err != nil {
		return err
	}
	a.enricher = cache

	return cache.Start(ctx)
}

// restMapper builds the mapper that turns a declared resource into
// the version and scope its informers need.
func (a *Agent) restMapper(ctx context.Context) (meta.RESTMapper, error) {
	ctx, cancel := context.WithTimeout(ctx, startupPhaseTimeout)
	defer cancel()

	return enricher.NewRESTMapper(ctx, a.client.Discovery())
}

// startSource opens the watches, resuming them from the saved positions.
func (a *Agent) startSource(ctx context.Context, state checkpoint.State) error {
	dispatchers := make(map[string]source.Dispatcher, len(a.fanouts))
	for watch, fanout := range a.fanouts {
		dispatchers[watch] = fanout
	}
	src, err := source.New(source.Options{
		Client:      a.client,
		Config:      a.cfg.Source,
		Dispatchers: dispatchers,
		Enricher:    a.enricher,
		Resume:      state.Watches,
		Metrics:     a.metrics.forSource(),
		Logger:      a.logger.With(slog.String("component", "source")),
	})
	if err != nil {
		return err
	}
	a.source = src

	watchCtx, cancel := context.WithCancel(ctx)
	a.stopWatches = cancel

	// The stop cancels the watches and waits for them to return, so the
	// final checkpoint write cannot race one still recording a position.
	go func() {
		defer cancel()
		defer close(a.sourceDone)

		if err := src.Run(watchCtx); err != nil {
			a.errs <- err
		}
	}()
	return nil
}

// triggerReload asks for a config reload. Requests coalesce, so
// several arriving at once run only one.
func (a *Agent) triggerReload() {
	select {
	case a.reload <- struct{}{}:
	default:
	}
}

// onSIGHUP triggers a reopen of the sink destinations, and a reload
// where the agent accepts one. They coalesce, so several arriving at
// once runs only one.
func (a *Agent) onSIGHUP() {
	select {
	case a.reopen <- struct{}{}:
	default:
	}
	if a.cfg.Service.HotReload {
		a.triggerReload()
	}
}

// reopenLoop reopens the sink destinations until ctx is canceled, one
// at a time.
//
// It runs on its own goroutine because reopening takes the destination
// lock, which a drainer inside a long write holds. Doing it on the signal
// goroutine would block every signal behind it, SIGTERM included.
func (a *Agent) reopenLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.reopen:
			a.reopenSinks(ctx)
		}
	}
}

// reopenSinks reacquires every sink destination, which is how a
// rotated file is followed.
func (a *Agent) reopenSinks(ctx context.Context) {
	for _, entry := range a.sinks {
		if entry.opener == nil {
			continue
		}
		if err := entry.opener.Open(ctx); err != nil {
			a.log.LogAttrs(ctx, slog.LevelError, "failed to reopen sink destination",
				slog.String("sink", entry.name),
				slog.Any("error", err),
			)
		}
	}
}

// nopEnricher is used when the configuration declares no resource.
type nopEnricher struct{}

// Enrich implements the [source.Enricher] interface.
func (nopEnricher) Enrich(context.Context, string, *event.Event) {}
