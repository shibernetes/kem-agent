package agent

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// watchSignals calls cancel on the first SIGTERM or SIGINT signal
// received, and ends the process on the second.
// The returned function can be called to stop the signal watcher.
func watchSignals(cancel context.CancelFunc, logger *slog.Logger) func() {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig, ok := <-ch
		if !ok {
			return
		}
		logger.LogAttrs(context.Background(), slog.LevelInfo,
			"signal received",
			slog.String("signal", sig.String()),
		)
		cancel()

		if _, ok := <-ch; !ok {
			return
		}
		logger.LogAttrs(context.Background(), slog.LevelWarn,
			"second signal received, exiting immediately",
		)
		os.Exit(1)
	}()
	return func() {
		signal.Stop(ch)
		close(ch)
	}
}

// relaySIGHUP calls notify on every SIGHUP until ctx is done.
func relaySIGHUP(ctx context.Context, notify func()) {
	ch := make(chan os.Signal, 1)

	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			notify()
		}
	}
}
