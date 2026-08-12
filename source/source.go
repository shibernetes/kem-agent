package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/shibernetes/kem-agent/source/sanitizer"
)

// Source reads the events of every configured watch.
// Each one runs on its own goroutine, so events keep their order
// per namespace and interleave across them.
type Source struct {
	watchers []*namespaceWatcher
	logger   *slog.Logger
}

// Options configures a [Source].
type Options struct {
	Client      kubernetes.Interface
	Config      Config
	Dispatchers map[string]Dispatcher
	Enricher    Enricher
	Resume      map[string]string
	Metrics     MetricsVec
	Logger      *slog.Logger
}

// New returns a source over the configured watches, each resuming
// from its checkpointed position. Every watch needs a dispatcher,
// keyed by the namespace it reads.
func New(opts Options) (*Source, error) {
	if opts.Enricher == nil {
		return nil, errors.New("source: an enricher is required")
	}
	var (
		src        = &Source{logger: opts.Logger}
		sanitizers = sanitizer.New(opts.Config.Sanitizers)
	)
	for _, cfg := range opts.Config.Watches {
		dispatcher, ok := opts.Dispatchers[cfg.Namespace]
		if !ok {
			return nil, fmt.Errorf("source: watch of namespace %q has no dispatcher", cfg.Namespace)
		}
		src.watchers = append(src.watchers, newWatcher(watcherOptions{
			client:     opts.Client,
			config:     cfg,
			dispatcher: dispatcher,
			enricher:   opts.Enricher,
			sanitizers: sanitizers,
			metrics:    opts.Metrics.forWatch(watchName(cfg.Namespace)),
			logger:     opts.Logger,
			resume:     opts.Resume[cfg.Namespace],
			maxAge:     time.Duration(opts.Config.BootstrapEventMaxAge),
		}))
	}
	return src, nil
}

// Run opens every watch and reads until the context is done, or until
// one fails in a way reconnecting cannot resolve, which stops the others
// with it. Every watch has ended once Run returns.
func (s *Source) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.logger.LogAttrs(ctx, slog.LevelInfo, "source started",
		slog.Int("watches", len(s.watchers)),
		slog.String("namespaces", s.watchedNamespaces()),
	)
	errs := make(chan error, len(s.watchers))

	var wg sync.WaitGroup
	for _, w := range s.watchers {
		wg.Go(func() {
			if err := w.run(ctx); err != nil {
				errs <- err
				cancel()
			}
		})
	}
	wg.Wait()

	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

// Positions returns the furthest position each watch has read to, keyed
// by namespace. A watch that holds no position yet is not reported.
func (s *Source) Positions() map[string]string {
	positions := make(map[string]string, len(s.watchers))

	for _, w := range s.watchers {
		if pos := w.position(); pos != "" {
			positions[w.config.Namespace] = pos
		}
	}
	return positions
}

func (s *Source) watchedNamespaces() string {
	names := make([]string, 0, len(s.watchers))

	for _, w := range s.watchers {
		names = append(names, w.name)
	}
	return strings.Join(names, ",")
}
