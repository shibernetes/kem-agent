package sink

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		err  error
		want Result
	}{
		"delivered":   {nil, ResultSuccess},
		"oversized":   {fmt.Errorf("rejected: %w", ErrOversized), ResultOversized},
		"permanent":   {fmt.Errorf("unauthorized: %w", ErrPermanent), ResultPermanent},
		"timeout":     {fmt.Errorf("deadline: %w", ErrSendTimeout), ResultTimeout},
		"not open":    {fmt.Errorf("%w: %s", ErrNotOpen, "collector:4317"), ResultPermanent},
		"closed":      {fmt.Errorf("%w: %s", ErrClosed, "collector:4317"), ResultPermanent},
		"unknown":     {errors.New("connection reset by peer"), ResultRetryable},
		"retry after": {NewRetryAfterError(errors.New("throttled"), time.Second), ResultRetryable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
