package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/sink"
)

var retryAfterNow = time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		err   error
		cause error
		want  sink.Result
	}{
		"delivered":              {nil, nil, sink.ResultSuccess},
		"network failure":        {errors.New("connection refused"), nil, sink.ResultRetryable},
		"deadline in the chain":  {fmt.Errorf("Post %q: %w", "https://foo.xyz", sink.ErrSendTimeout), sink.ErrSendTimeout, sink.ResultTimeout},
		"deadline only in cause": {errors.New("i/o timeout"), sink.ErrSendTimeout, sink.ResultTimeout},
		"cancelled elsewhere":    {errors.New("context canceled"), context.Canceled, sink.ResultRetryable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := sink.Classify(classify(canceledWithCause(t, tc.cause), tc.err)); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// TestClassifyWrapsOnce asserts that a deadline the client already
// reported is not wrapped twice, since the client is already using
// the cause it was given in the error it returns.
func TestClassifyWrapsOnce(t *testing.T) {
	err := fmt.Errorf("Post %q: %w", "https://foo.xyz", sink.ErrSendTimeout)

	got := classify(canceledWithCause(t, sink.ErrSendTimeout), err)
	if got.Error() != err.Error() {
		t.Errorf("got %q, want %q", got, err)
	}
	if n := strings.Count(got.Error(), sink.ErrSendTimeout.Error()); n != 1 {
		t.Errorf("the error message contains the cause %d times, want once", n)
	}
}

func TestClassifyStatus(t *testing.T) {
	cases := map[int]sink.Result{
		http.StatusOK:                    sink.ResultSuccess,
		http.StatusCreated:               sink.ResultSuccess,
		http.StatusAccepted:              sink.ResultSuccess,
		http.StatusNoContent:             sink.ResultSuccess,
		http.StatusMovedPermanently:      sink.ResultPermanent,
		http.StatusFound:                 sink.ResultPermanent,
		http.StatusTemporaryRedirect:     sink.ResultPermanent,
		http.StatusBadRequest:            sink.ResultPermanent,
		http.StatusUnauthorized:          sink.ResultPermanent,
		http.StatusForbidden:             sink.ResultPermanent,
		http.StatusNotFound:              sink.ResultPermanent,
		http.StatusRequestTimeout:        sink.ResultRetryable,
		http.StatusGone:                  sink.ResultPermanent,
		http.StatusRequestEntityTooLarge: sink.ResultOversized,
		http.StatusUnsupportedMediaType:  sink.ResultPermanent,
		http.StatusUnprocessableEntity:   sink.ResultPermanent,
		http.StatusTooManyRequests:       sink.ResultRetryable,
		http.StatusInternalServerError:   sink.ResultRetryable,
		http.StatusNotImplemented:        sink.ResultPermanent,
		http.StatusBadGateway:            sink.ResultRetryable,
		http.StatusServiceUnavailable:    sink.ResultRetryable,
		http.StatusGatewayTimeout:        sink.ResultRetryable,
	}
	for status, want := range cases {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			resp := statusResponse(status, nil)

			err := classifyStatus(&resp, retryAfterNow)
			if got := sink.Classify(err); got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
}

func TestClassifyStatusHasRequestedDelay(t *testing.T) {
	var (
		resp = statusResponse(http.StatusTooManyRequests, http.Header{"Retry-After": {"30"}})
		err  = classifyStatus(&resp, retryAfterNow)
	)
	retry, ok := errors.AsType[*sink.RetryAfterError](err)
	if !ok {
		t.Fatalf("got %T, want a *sink.RetryAfterError", err)
	}
	if retry.Delay != 30*time.Second {
		t.Errorf("got %s delay, want 30s", retry.Delay)
	}
}

// TestClassifyStatusIgnoresDelayWhenPermanent asserts that a RetryAfterError
// is returned only if the underlying error is retryable.
func TestClassifyStatusIgnoresDelayWhenPermanent(t *testing.T) {
	resp := statusResponse(http.StatusBadRequest, http.Header{"Retry-After": {"15"}})

	err := classifyStatus(&resp, retryAfterNow)
	if _, ok := errors.AsType[*sink.RetryAfterError](err); ok {
		t.Errorf("got a delay on a permanent failure, want none: %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := map[string]struct {
		value string
		want  time.Duration
	}{
		"seconds":     {"120", 2 * time.Minute},
		"one second":  {"1", time.Second},
		"zero":        {"0", 0},
		"signed":      {"-5", 0},
		"unparsable":  {"soon", 0},
		"absent":      {"", 0},
		"rfc1123":     {retryAfterNow.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		"ansi c":      {retryAfterNow.Add(time.Hour).Format(time.ANSIC), time.Hour},
		"date passed": {retryAfterNow.Add(-time.Minute).Format(http.TimeFormat), 0},
		"date now":    {retryAfterNow.Format(http.TimeFormat), 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			delay, ok := parseRetryAfter(tc.value, retryAfterNow)
			switch {
			case tc.want == 0 && ok:
				t.Errorf("got %s delay, want none", delay)
			case tc.want != 0 && !ok:
				t.Error("got no delay, want one")
			case delay != tc.want:
				t.Errorf("got %s, want %s", delay, tc.want)
			}
		})
	}
}

func canceledWithCause(t *testing.T, cause error) context.Context {
	t.Helper()

	if cause == nil {
		return t.Context()
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)

	return ctx
}

func statusResponse(status int, header http.Header) http.Response {
	if header == nil {
		header = http.Header{}
	}
	return http.Response{
		StatusCode: status,
		Header:     header,
	}
}
