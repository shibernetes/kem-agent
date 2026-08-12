package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/logsample"
	"github.com/shibernetes/kem-agent/source/sanitizer"
)

const (
	watchFailureInterval  = time.Minute
	watchFailureThreshold = 3
	minRestartDelay       = time.Second
	maxRestartDelay       = 30 * time.Second
	restartJitter         = 0.5
	minWatchTimeout       = 5 * time.Minute
)

var (
	errRetryable = errors.New("watch failed")
	errExpired   = errors.New("resume position expired")
)

// A Dispatcher dispatches an event to the pipelines bound to a watch.
type Dispatcher interface {
	Dispatch(context.Context, *event.Event)
}

// An Enricher attaches the metadata of the object an event regards.
type Enricher interface {
	Enrich(ctx context.Context, watch string, ev *event.Event)
}

// namespaceWatcher reads and dispatches the events of one namespace,
// or of every namespace when none is configured.
//
// It records how far its stream was read, so a restart resumes from
// the last position rather than replaying everything the APIServer
// retains.
type namespaceWatcher struct {
	client     kubernetes.Interface
	config     WatchConfig
	name       string
	dispatcher Dispatcher
	enricher   Enricher
	sanitizers *sanitizer.Set
	replay     replay
	metrics    watchMetrics
	logger     *slog.Logger
	failures   *logsample.Gate[metav1.StatusReason]
	mu         sync.Mutex
	pos        string
	recovering bool
}

// watcherOptions configures a namespaceWatcher.
type watcherOptions struct {
	client     kubernetes.Interface
	config     WatchConfig
	dispatcher Dispatcher
	enricher   Enricher
	sanitizers *sanitizer.Set
	metrics    watchMetrics
	logger     *slog.Logger
	resume     string
	maxAge     time.Duration
}

// newWatcher returns a watch resuming from the given position,
// or replaying the events the APIServer retains before streaming
// when none is given.
func newWatcher(opts watcherOptions) *namespaceWatcher {
	w := &namespaceWatcher{
		client:     opts.client,
		config:     opts.config,
		name:       watchName(opts.config.Namespace),
		dispatcher: opts.dispatcher,
		enricher:   opts.enricher,
		sanitizers: opts.sanitizers,
		replay:     replay{maxAge: opts.maxAge},
		metrics:    opts.metrics,
		logger:     opts.logger,
		failures:   logsample.PerInterval[metav1.StatusReason](watchFailureInterval, watchFailureThreshold),
		pos:        opts.resume,
	}
	if opts.resume == "" {
		w.replay.start()
	}
	return w
}

// run keeps the watch open until the context is done.
// It retries automatically for errors a new connection can resolve,
// and returns the ones for which it does not.
func (w *namespaceWatcher) run(ctx context.Context) error {
	var delay time.Duration

	for {
		start := time.Now()
		err := w.watchOnce(ctx)

		if ctx.Err() != nil {
			return nil
		}
		elapsed := time.Since(start)

		switch {
		case err == nil:
			w.metrics.restartClosed.Inc()
		case errors.Is(err, errRetryable):
			w.metrics.restartError.Inc()
		case errors.Is(err, errExpired):
			w.expire()
		default:
			return err
		}
		if err == nil || elapsed >= maxRestartDelay {
			delay = minRestartDelay
		} else {
			delay = min(max(delay*2, minRestartDelay), maxRestartDelay)
		}
		// The APIServer closes a healthy watch periodically by
		// design, which is the ordinary case, and reconnects
		// at once.
		if err == nil && elapsed >= minRestartDelay {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait.Jitter(delay, restartJitter)):
		}
	}
}

// watchOnce opens a watch and reads its stream until it ends.
func (w *namespaceWatcher) watchOnce(ctx context.Context) error {
	stream, err := w.open(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return w.classify(ctx, err)
	}
	defer func() {
		stream.Stop()
	}()
	if w.recovering {
		w.logger.LogAttrs(ctx, slog.LevelInfo, "watch reconnected",
			slog.String("namespace", w.name),
		)
		w.recovering = false
	}
	return w.read(ctx, stream)
}

// open starts a watch from the position it last read or from the
// state the APIServer holds when a replay is started.
func (w *namespaceWatcher) open(ctx context.Context) (watch.Interface, error) {
	opts := metav1.ListOptions{
		LabelSelector:       w.config.LabelSelector,
		FieldSelector:       w.config.FieldSelector,
		AllowWatchBookmarks: true,
		ResourceVersion:     w.position(),
		TimeoutSeconds:      new(int64(wait.Jitter(minWatchTimeout, 1).Seconds())),
	}
	if w.replay.active {
		w.replay.restart()

		// The APIServer serves the initial events again from
		// the start, and rejects either streaming option given
		// without the other.
		opts.ResourceVersion = ""
		opts.ResourceVersionMatch = metav1.ResourceVersionMatchNotOlderThan
		opts.SendInitialEvents = new(true)
	}
	return w.client.EventsV1().Events(w.config.Namespace).Watch(ctx, opts)
}

// read reads the frames of a stream until it ends or the context is done.
func (w *namespaceWatcher) read(ctx context.Context, stream watch.Interface) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case frame, ok := <-stream.ResultChan():
			if !ok {
				return nil
			}
			if frame.Type == watch.Error {
				return w.classify(ctx, apierrors.FromObject(frame.Object))
			}
			w.handle(ctx, frame)
		}
	}
}

// handle processes one frame of the watch stream. Every frame advances
// the position, bookmarks and deletions included, so it moves with the
// stream rather than with the events that happen to be delivered.
//
// A replay advances nothing until it ends. Its events are served in name
// order rather than by resource version, so a position taken during one
// sits above events it has not reached yet.
func (w *namespaceWatcher) handle(ctx context.Context, frame watch.Event) {
	e, ok := frame.Object.(*eventsv1.Event)
	if !ok {
		return
	}
	switch frame.Type {
	case watch.Added, watch.Modified:
		w.deliver(ctx, e)
	case watch.Bookmark:
		// The bookmark ending a replay carries the position
		// the whole replay covers, which is the first one the
		// watch records.
		if e.Annotations[metav1.InitialEventsAnnotationKey] == "true" {
			w.endReplay(ctx)
		}
	case watch.Deleted:
		// The APIServer garbage collects an event an hour after
		// it was written, and the deletion carries the whole object,
		// so passing it on would deliver every event a second time.
	}
	if !w.replay.active && e.ResourceVersion != "" {
		w.advance(e.ResourceVersion)
	}
}

// deliver lifts an event and dispatches it to the pipelines bound to the watch.
func (w *namespaceWatcher) deliver(ctx context.Context, e *eventsv1.Event) {
	ev := lift(e)
	if !w.replay.keep(ev) {
		return
	}
	w.sanitizers.Sanitize(ev)
	w.enricher.Enrich(ctx, w.config.Namespace, ev)
	w.metrics.read.Inc()
	w.dispatcher.Dispatch(ctx, ev)
}

// endReplay ends the replay and reports what it delivered.
func (w *namespaceWatcher) endReplay(ctx context.Context) {
	isBootstrap := w.replay.floor == ""
	replayed, skipped := w.replay.end()

	// A replay after an expired position is the expected behavior for a
	// namespace that produces no events, so it is reported only when it
	// delivered events the watch had missed.
	if !isBootstrap && replayed == 0 {
		return
	}
	w.logger.LogAttrs(ctx,
		slog.LevelInfo, "watch replay complete",
		slog.String("namespace", w.name),
		slog.Int("replayed", replayed),
		slog.Int("skipped", skipped),
	)
}

// classify decides how a watch failure is handled, and reports
// the ones that reconnecting does not resolve.
//
// Everything but a refused streaming list option is retried, an
// authorization failure included, because one namespace losing
// its stream must not stop the agent reading the others.
func (w *namespaceWatcher) classify(ctx context.Context, err error) error {
	switch {
	case apierrors.IsResourceExpired(err), apierrors.IsGone(err):
		return errExpired
	case rejectedStreamingList(err):
		return fmt.Errorf(
			"watch of namespace %q was rejected, the APIServer does not serve initial events: %w",
			w.name, err,
		)
	}
	w.recovering = true

	if suppressed, ok := w.failures.Admit(apierrors.ReasonForError(err)); ok {
		w.logger.LogAttrs(ctx, slog.LevelWarn, "watch failed, retrying",
			slog.String("namespace", w.name),
			slog.Any("error", err),
			suppressed,
		)
	}
	return errRetryable
}

// expire replays the events with a resource version newer than the last one
// the watch read, which the APIServer no longer holds. The watch keeps that
// last resource version recorded until the replay ends, so a checkpoint
// written in the meantime still holds it.
//
// An expiry is expected for a namespace that produces no events, so it is
// counted but not logged.
func (w *namespaceWatcher) expire() {
	w.metrics.restartExpired.Inc()
	w.replay.startAfter(w.position())
}

// position returns how far the stream was read, or an empty string
// when the watch holds no position.
func (w *namespaceWatcher) position() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pos
}

// advance records how far the stream was read.
func (w *namespaceWatcher) advance(rv string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pos = rv
}

// rejectedStreamingList reports whether the APIServer refused the
// streaming list options a replay depends on. They are only valid
// when they are used together, and the WatchList feature gate is
// what enables those options.
func rejectedStreamingList(err error) bool {
	if !apierrors.IsInvalid(err) {
		return false
	}
	cause, ok := apierrors.StatusCause(err, metav1.CauseTypeForbidden)

	return ok && (cause.Field == "sendInitialEvents" || cause.Field == "resourceVersionMatch")
}
