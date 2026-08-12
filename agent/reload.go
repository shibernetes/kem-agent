package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/shibernetes/kem-agent/config"
	"github.com/shibernetes/kem-agent/internal/configwatcher"
)

// watchConfigFile triggers a reload whenever the config file changes.
func (a *Agent) watchConfigFile(ctx context.Context) error {
	watcher, err := configwatcher.New(a.triggerReload, a.configPath)
	if err != nil {
		return fmt.Errorf("failed to watch config file: %w", err)
	}
	go func() {
		if err := watcher.Watch(ctx, a.log); err != nil {
			a.log.LogAttrs(ctx, slog.LevelError, "config watcher stopped",
				slog.Any("error", err),
			)
		}
	}()
	return nil
}

// reloadLoop applies config changes until ctx is canceled, one at a time.
func (a *Agent) reloadLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.reload:
			a.reloadConfig(ctx)
		}
	}
}

// reloadConfig re-reads the config file and applies the changes, if any.
// Any failure keeps the running config in place and leaves the last
// applied bytes unchanged.
//
// Only the filters are swapped. A change to anything else is reported and
// needs a restart, so until then the agent runs the new filters with the
// old settings.
func (a *Agent) reloadConfig(ctx context.Context) {
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		a.reloadFailed(ctx, "failed to read config file", nil, err)
		return
	}
	// A change to any key of a mounted ConfigMap notifies the watcher,
	// so the bytes are compared before anything is parsed.
	if bytes.Equal(data, a.lastConfig) {
		return
	}
	next, err := config.Parse(data, a.factories)
	if err != nil {
		if next != nil {
			a.logWarnings(ctx, next)
		}
		a.reloadFailed(ctx, "failed to load configuration", nil, err)
		return
	}
	delta, blocks := config.ClassifyDelta(a.cfg, next.Config)

	if err := a.swapFilters(next.Config); err != nil {
		a.reloadFailed(ctx, "failed to compile filters", next, err)
		return
	}
	a.logWarnings(ctx, next)
	a.lastConfig = data

	switch delta {
	case config.DeltaStructural:
		a.log.LogAttrs(ctx, slog.LevelWarn,
			"config changed outside the filters, restart the agent to apply it",
			slog.Any("blocks", blocks),
		)
		a.metrics.reloads.WithLabelValues("partial").Inc()
	case config.DeltaFilters:
		a.log.LogAttrs(ctx, slog.LevelInfo, "filters reloaded")
		a.metrics.reloads.WithLabelValues("success").Inc()
	case config.DeltaNone:
		a.metrics.reloads.WithLabelValues("success").Inc()
	}
}

// swapFilters compiles every pipeline's filters and installs them all
// at once, so no event is evaluated against a mixture of two configs.
//
// A pipeline holds its index for the life of the process, so a pipeline
// the new config no longer declares keeps the filters it runs.
func (a *Agent) swapFilters(next *config.Config) error {
	compiled, err := compileFilters(a.filterEnv, next.Pipelines)
	if err != nil {
		return err
	}
	a.filters.Store(new(orderFilterSets(a.pipelines, compiled, *a.filters.Load())))

	return nil
}

// logWarnings logs every warning a parsed config raises.
func (a *Agent) logWarnings(ctx context.Context, res *config.Result) {
	for _, warning := range ConfigWarnings(res) {
		a.log.LogAttrs(ctx, slog.LevelWarn, warning.Message,
			ConfigWarningAttrs(a.configPath, warning)...)
	}
}

// reloadFailed reports a failed reload, which leaves the running
// config in place.
func (a *Agent) reloadFailed(ctx context.Context, msg string, parsed *config.Result, err error) {
	a.log.LogAttrs(ctx, slog.LevelWarn, msg, ConfigFailureAttrs(parsed, a.configPath, err)...)
	a.metrics.reloads.WithLabelValues("failure").Inc()
}
