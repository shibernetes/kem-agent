package source

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var eventGroupResource = schema.GroupResource{Group: "events.k8s.io", Resource: "events"}

func TestWatcherPosition(t *testing.T) {
	w := newTestWatcher(t, watcherOptions{resume: "42"})

	if got := w.position(); got != "42" {
		t.Errorf("got position %q, want the one it resumed from", got)
	}
	w.advance("43")
	if got := w.position(); got != "43" {
		t.Errorf("got position %q, want %q", got, "43")
	}
	w.advance("")
	if got := w.position(); got != "" {
		t.Errorf("got position %q, want it cleared", got)
	}
}

func TestWatcherStartsReplayWithoutPosition(t *testing.T) {
	if w := newTestWatcher(t, watcherOptions{}); !w.replay.active {
		t.Error("a watcher holding no position does not replay, want it replaying")
	}
	if w := newTestWatcher(t, watcherOptions{resume: "1042"}); w.replay.active {
		t.Error("a watcher resuming from a position replays, want it streaming")
	}
}

// TestWatcherOpenOptions asserts that a replay sets both streaming
// options with no resource version, while a resuming watch sets neither.
// The APIServer rejects either one given without the other.
func TestWatcherOpenOptions(t *testing.T) {
	cases := map[string]struct {
		config WatchConfig
		resume string
		want   metav1.ListOptions
	}{
		"resuming from a position": {
			config: WatchConfig{Namespace: testNamespace},
			resume: "1042",
			want: metav1.ListOptions{
				Watch:               true,
				AllowWatchBookmarks: true,
				ResourceVersion:     "1042",
			},
		},
		"replaying from the start": {
			config: WatchConfig{Namespace: testNamespace},
			want: metav1.ListOptions{
				Watch:                true,
				AllowWatchBookmarks:  true,
				ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
				SendInitialEvents:    new(true),
			},
		},
		"selectors passed through": {
			config: WatchConfig{
				Namespace:     testNamespace,
				LabelSelector: "app in (web, api)",
				FieldSelector: `regarding.name=a\,b`,
			},
			resume: "1042",
			want: metav1.ListOptions{
				Watch:               true,
				AllowWatchBookmarks: true,
				ResourceVersion:     "1042",
				LabelSelector:       "app in (web, api)",
				FieldSelector:       `regarding.name=a\,b`,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var (
				fw = newFakeWatch()
				w  = newTestWatcher(t, watcherOptions{
					client: fw.clientset(),
					config: tc.config,
					resume: tc.resume,
				})
			)
			if _, err := w.open(t.Context()); err != nil {
				t.Fatalf("failed to open watch: %v", err)
			}
			// The timeout is random, and TestWatcherOpenTimeout covers it.
			got := fw.options(t, 0)
			got.TimeoutSeconds = nil
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("the watch options differ (-want +got):\n%s", diff)
			}
		})
	}
}

// TestWatcherOpenTimeout asserts that every watch uses a jittered timeout
// between one and two times the minimum, randomized again at each reopen,
// so that watches opened together do not reconnect at the same time.
func TestWatcherOpenTimeout(t *testing.T) {
	var (
		fw = newFakeWatch()
		w  = newTestWatcher(t, watcherOptions{
			client: fw.clientset(),
			config: WatchConfig{Namespace: testNamespace},
			resume: "1042",
		})
		seen = make(map[int64]bool)
	)
	for range 20 {
		if _, err := w.open(t.Context()); err != nil {
			t.Fatalf("failed to open watch: %v", err)
		}
	}
	for n := range fw.calls() {
		timeout := fw.options(t, n).TimeoutSeconds
		if timeout == nil {
			t.Fatalf("watch %d was opened with no timeout, want one", n)
		}
		got := time.Duration(*timeout) * time.Second
		if got < minWatchTimeout || got > 2*minWatchTimeout {
			t.Errorf("got a timeout of %s, want it between %s and %s", got, minWatchTimeout, 2*minWatchTimeout)
		}
		seen[*timeout] = true
	}
	if len(seen) < 2 {
		t.Error("every watch asked for the same timeout, want them randomized")
	}
}

func TestWatcherOpenRestartsReplay(t *testing.T) {
	var (
		fw = newFakeWatch()
		w  = newTestWatcher(t, watcherOptions{
			client: fw.clientset(),
			config: WatchConfig{Namespace: testNamespace},
		})
	)
	w.replay.startAfter("1000")
	w.replay.keep(eventWithVersion("1001"))

	if _, err := w.open(t.Context()); err != nil {
		t.Fatalf("failed to open watch: %v", err)
	}
	if w.replay.kept != 0 {
		t.Errorf("got %d kept, want the count reset", w.replay.kept)
	}
	// The first event checks that the position was not cleared, and the
	// second that what was written after it is still delivered.
	if w.replay.keep(eventWithVersion("999")) {
		t.Error("an event below the position was kept, want it dropped")
	}
	if !w.replay.keep(eventWithVersion("1001")) {
		t.Error("an event above the position was dropped, want it kept")
	}
}

func TestWatcherClassifyRetries(t *testing.T) {
	cases := map[string]struct {
		err  error
		want error
	}{
		"gone":                    {err: goneError(), want: errExpired},
		"resource expired":        {err: apierrors.NewResourceExpired("resource expired"), want: errExpired},
		"unauthorized":            {err: apierrors.NewUnauthorized("unauthorized"), want: errRetryable},
		"forbidden":               {err: apierrors.NewForbidden(eventGroupResource, "", errors.New("forbidden")), want: errRetryable},
		"server error":            {err: apierrors.NewInternalError(errors.New("internal error")), want: errRetryable},
		"network failure":         {err: errors.New("connection refused"), want: errRetryable},
		"invalid with no cause":   {err: invalidError("", ""), want: errRetryable},
		"invalid on other field":  {err: invalidError(metav1.CauseTypeForbidden, "labelSelector"), want: errRetryable},
		"invalid for other cause": {err: invalidError(metav1.CauseTypeFieldValueInvalid, "sendInitialEvents"), want: errRetryable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := newTestWatcher(t, watcherOptions{config: WatchConfig{Namespace: testNamespace}})

			if got := w.classify(t.Context(), tc.err); !errors.Is(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWatcherClassifyRefusedStreamingList(t *testing.T) {
	cases := map[string]string{
		"send initial events":    "sendInitialEvents",
		"resource version match": "resourceVersionMatch",
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			w := newTestWatcher(t, watcherOptions{config: WatchConfig{Namespace: testNamespace}})

			err := w.classify(t.Context(), invalidError(metav1.CauseTypeForbidden, field))
			if errors.Is(err, errRetryable) || errors.Is(err, errExpired) {
				t.Fatalf("got %v, want a fatal error", err)
			}
			if !strings.Contains(err.Error(), testNamespace) {
				t.Errorf("got %q, want the error to name the namespace", err)
			}
		})
	}
}

func TestWatcherSanitizesOnlyDeliveredEvents(t *testing.T) {
	cases := map[string]struct {
		rv        string
		sanitized bool
	}{
		"delivered": {rv: "1042", sanitized: true},
		"dropped":   {rv: "999"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := newTestWatcher(t, watcherOptions{
				config:     WatchConfig{Namespace: testNamespace},
				dispatcher: &eventRecorder{},
			})
			w.replay.startAfter("1000")

			e := watchedEvent(tc.rv)
			e.Annotations = map[string]string{corev1.LastAppliedConfigAnnotation: "{}"}

			w.deliver(t.Context(), e)

			_, ok := e.Annotations[corev1.LastAppliedConfigAnnotation]
			switch {
			case tc.sanitized && ok:
				t.Error("a delivered event kept its last-applied annotation, want it dropped")
			case !tc.sanitized && !ok:
				t.Error("a dropped event was sanitized, want it skipped")
			}
		})
	}
}

// TestWatcherSuppressesRepeatedFailures asserts that a watch failing
// over and over logs a bounded number of lines.
func TestWatcherSuppressesRepeatedFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			r = &logRecorder{}
			w = newTestWatcher(t, watcherOptions{
				config: WatchConfig{Namespace: testNamespace},
				logger: r.logger(),
			})
		)
		for range 5 {
			_ = w.classify(t.Context(), apierrors.NewInternalError(errors.New("oops")))
		}
		if got := r.count(); got != watchFailureThreshold {
			t.Fatalf("got %d lines over 5 failures, want %d", got, watchFailureThreshold)
		}
		// A failure for another cause is new information, so it should
		// be logged at once and report how many lines were suppressed.
		_ = w.classify(t.Context(), apierrors.NewUnauthorized("unauthorized"))

		if got := r.count(); got != watchFailureThreshold+1 {
			t.Fatalf("got %d lines, want a differing cause logged immediately", got)
		}
		if got := r.attr(t, watchFailureThreshold, "suppressed"); got != "2" {
			t.Errorf("the line reports %q records suppressed, want %q", got, "2")
		}
	})
}

// TestWatcherLogsFailuresAgainAfterInterval asserts that a failure lasting
// longer than the interval is logged again, rather than silenced permanently.
func TestWatcherLogsFailuresAgainAfterInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			r = &logRecorder{}
			w = newTestWatcher(t, watcherOptions{
				config: WatchConfig{Namespace: testNamespace},
				logger: r.logger(),
			})
		)
		err := apierrors.NewInternalError(errors.New("oops"))

		for range 5 {
			_ = w.classify(t.Context(), err)
		}
		time.Sleep(2 * watchFailureInterval)
		_ = w.classify(t.Context(), err)

		if got := r.count(); got != watchFailureThreshold+1 {
			t.Fatalf("got %d lines, want the failure reported again", got)
		}
		if got := r.attr(t, watchFailureThreshold, "suppressed"); got != "2" {
			t.Errorf("the line reports %q records suppressed, want %q", got, "2")
		}
	})
}

// goneError returns the Gone status the APIServer sends when the
// resource version a watch resumes from is no longer available.
func goneError() error {
	return &apierrors.StatusError{ErrStatus: *goneStatus()}
}

// goneStatus returns the status an APIServer sends as an error frame when
// the resource version a watch resumed from is no longer held.
func goneStatus() *metav1.Status {
	return &metav1.Status{
		Status: metav1.StatusFailure,
		Code:   http.StatusGone,
		Reason: metav1.StatusReasonGone,
	}
}

// invalidError returns the Invalid status the APIServer sends
// for a refused watch option.
func invalidError(cause metav1.CauseType, field string) error {
	status := metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusUnprocessableEntity,
		Reason:  metav1.StatusReasonInvalid,
		Message: "Event is invalid",
	}
	if cause != "" {
		status.Details = &metav1.StatusDetails{
			Causes: []metav1.StatusCause{{Type: cause, Field: field}},
		}
	}
	return &apierrors.StatusError{ErrStatus: status}
}
