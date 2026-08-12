package enricher

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/tools/cache"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/logsample"
	"github.com/shibernetes/kem-agent/source/sanitizer"
)

const (
	maxWarnedKeys = 128
	maxKeyBytes   = validation.DNS1123SubdomainMaxLength
)

// Cache holds the metadata of the objects events regard.
type Cache struct {
	resources   map[schema.GroupKind]*resource
	syncTimeout time.Duration
	metrics     Metrics
	logger      *slog.Logger
	undeclared  *logsample.Gate[schema.GroupKind]
	crossNS     *logsample.Gate[namespacePair]
}

// Options configures how a [Cache] is built.
type Options struct {
	Client     metadata.Interface
	Mapper     meta.RESTMapper
	Watches    []string
	Sanitizers []sanitizer.MetadataSanitizer
	Metrics    Metrics
	Logger     *slog.Logger
}

// namespacePair is the pair of namespaces a refused lookup is
// reported for, the one the event was created in and the one
// it refers to with the regarding object.
type namespacePair struct {
	event     string
	regarding string
}

// New returns a cache of the declared resources, each resolved through
// the mapper.
//
// A resource that cannot be resolved by the mapper is not available in
// the target cluster, which can be the case for CRDs. Those are reported
// and skipped, rather than refused.
func New(ctx context.Context, cfg Config, opts Options) (*Cache, error) {
	transform := &transformer{
		labels:      cfg.Labels,
		annotations: cfg.Annotations,
		sanitizers:  opts.Sanitizers,
		mapper:      opts.Mapper,
	}
	resources := make(map[schema.GroupKind]*resource, len(cfg.Resources))

	for _, name := range cfg.Resources {
		r, err := resolve(opts.Mapper, name)
		if err != nil {
			opts.Logger.LogAttrs(ctx, slog.LevelWarn, "enrichment resource skipped",
				slog.String("resource", name),
				slog.Any("error", err),
			)
			continue
		}
		// Two names can resolve to one kind, which the configuration
		// cannot rule out without the cluster to resolve them against.
		if _, ok := resources[r.kind]; ok {
			return nil, fmt.Errorf("enrichment: resource %q resolves to %s, which is already declared", name, r.kind)
		}
		if err := r.build(opts.Client, opts.Watches, transform); err != nil {
			return nil, err
		}
		resources[r.kind] = r
	}
	return &Cache{
		resources:   resources,
		syncTimeout: time.Duration(cfg.SyncTimeout),
		metrics:     opts.Metrics,
		logger:      opts.Logger,
		crossNS:     logsample.FirstSeen[namespacePair](maxWarnedKeys),
		undeclared:  logsample.FirstSeen[schema.GroupKind](maxWarnedKeys),
	}, nil
}

// Start runs every informer and waits for the caches to be filled.
// A single timeout covers all of them, because a timeout applied to each
// informer separately would let the total reach that timeout multiplied
// by the number of informers.
//
// A cache that fails to fill in time is a fatal error. Enrichment is opt-in,
// so a declared resource is an explicit intent, and events that are shipped
// without it cannot be enriched later on.
func (c *Cache) Start(ctx context.Context) error {
	if len(c.resources) == 0 {
		return nil
	}
	var synced []cache.InformerSynced

	for _, r := range c.resources {
		for _, informer := range r.informers {
			go informer.RunWithContext(ctx)
			synced = append(synced, informer.HasSynced)
		}
	}
	syncCtx, cancel := context.WithTimeout(ctx, c.syncTimeout)
	defer cancel()

	if !cache.WaitForCacheSync(syncCtx.Done(), synced...) {
		// A stop closes the same channel the timeout does, and reporting
		// it as a timeout sends an operator after a limit that held.
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("enrichment: caches sync timeout after %s: %s",
			c.syncTimeout, strings.Join(c.unsynced(), ", "),
		)
	}
	c.logObjectCounts(ctx)

	return nil
}

// Enrich updates an event to set the metadata of the object it regards.
func (c *Cache) Enrich(ctx context.Context, watch string, ev *event.Event) {
	if len(c.resources) == 0 {
		return
	}
	ref := &ev.Regarding
	if incompleteRef(ref) {
		c.metrics.Skipped.Inc()
		return
	}
	gk := groupKindOf(ref)

	r, ok := c.resources[gk]
	if !ok {
		c.metrics.Undeclared.Inc()
		c.warnUndeclared(ctx, gk)
		return
	}
	// A namespaced object is resolved strictly within the namespace of
	// the event, because an event reporter sets the namespace freely, which
	// would otherwise expose the metadata of another tenant's objects.
	if r.namespaced && ref.Namespace != ev.Namespace {
		c.metrics.CrossNamespace.Inc()
		c.warnCrossNamespace(ctx, ev.Namespace, ref.Namespace)
		return
	}
	e := r.lookup(watch, ref)
	if e == nil {
		c.metrics.Miss.Inc()
		return
	}
	ev.RegardingObject = e.object
	c.metrics.Hit.Inc()
}

// unsynced returns the list of caches that have not been synced.
func (c *Cache) unsynced() []string {
	var names []string

	for _, r := range c.resources {
		for ns, informer := range r.informers {
			if !informer.HasSynced() {
				names = append(names, r.name(ns))
			}
		}
	}
	// Informers are held in maps, so the order they are read
	// in is not the order they were declared in.
	slices.Sort(names)

	return names
}

// logObjectCounts reports the number of objects held by each informer.
// The memory used by informers is proportional to the number of objects
// in the cluster. The cost is reported at startup rather than discovered
// through an OOMKill.
func (c *Cache) logObjectCounts(ctx context.Context) {
	for _, r := range c.resources {
		for ns, informer := range r.informers {
			// A [cache.Store] cannot report the count of items it
			// holds, so its keys are listed and counted instead.
			c.logger.LogAttrs(ctx, slog.LevelInfo, "enrichment cache filled",
				slog.String("namespace", ns),
				slog.String("resource", r.gvr.GroupResource().String()),
				slog.Int("objects", len(informer.GetIndexer().ListKeys())),
			)
		}
	}
}

// warnUndeclared reports an event that regards a kind no declared
// resource covers.
func (c *Cache) warnUndeclared(ctx context.Context, gk schema.GroupKind) {
	key := schema.GroupKind{
		Group: truncate(gk.Group),
		Kind:  truncate(gk.Kind),
	}
	if suppressed, ok := c.undeclared.Admit(key); ok {
		c.logger.LogAttrs(ctx, slog.LevelWarn, "event regards an undeclared resource",
			slog.String("group", key.Group),
			slog.String("kind", key.Kind),
			suppressed,
		)
	}
}

// warnCrossNamespace reports an event that regards an object in
// another namespace, which is never resolved.
func (c *Cache) warnCrossNamespace(ctx context.Context, ev, regarding string) {
	key := namespacePair{
		event:     ev,
		regarding: truncate(regarding),
	}
	if suppressed, ok := c.crossNS.Admit(key); ok {
		c.logger.LogAttrs(ctx, slog.LevelWarn, "event regards an object in another namespace",
			slog.String("namespace", key.event),
			slog.String("regardingNamespace", key.regarding),
			suppressed,
		)
	}
}

func truncate(s string) string {
	if len(s) <= maxKeyBytes {
		return s
	}
	limit := maxKeyBytes
	for i := 0; i < utf8.UTFMax-1 && limit > 0 && !utf8.RuneStart(s[limit]); i++ {
		limit--
	}
	return strings.Clone(s[:limit])
}
