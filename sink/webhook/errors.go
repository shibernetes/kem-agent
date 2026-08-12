package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/shibernetes/kem-agent/sink"
)

// classify turns a transport error into a delivery error.
func classify(ctx context.Context, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sink.ErrSendTimeout):
		// The client reports the cause the attempt context was wrapped
		// with, so a deadline arrives with the sentinel already in the
		// chain and wrapping it again would repeat it.
		return err
	case errors.Is(context.Cause(ctx), sink.ErrSendTimeout):
		// A RoundTripper returning its own deadline error leaves nothing
		// in the error chain to match, so the cause identifies it.
		return fmt.Errorf("%w: %w", sink.ErrSendTimeout, err)
	}
	return err
}

// classifyStatus turns an HTTP response status into a delivery outcome.
func classifyStatus(resp *http.Response, now time.Time) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusRequestEntityTooLarge:
		return fmt.Errorf("%w: request failed with status %d", sink.ErrOversized, resp.StatusCode)
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500 && resp.StatusCode != http.StatusNotImplemented:
		err := fmt.Errorf("request failed with status %d", resp.StatusCode)

		if delay, ok := parseRetryAfter(resp.Header.Get("Retry-After"), now); ok {
			return sink.NewRetryAfterError(err, delay)
		}
		return err
	}
	return fmt.Errorf("%w: request failed with status %d", sink.ErrPermanent, resp.StatusCode)
}

// parseRetryAfter parses a Retry-After header value, either a count
// of seconds or an HTTP-date, into a positive delay from now.
// It returns false when the value is absent, malformed, or outdated.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if delay := at.Sub(now); delay > 0 {
		return delay, true
	}
	return 0, false
}
