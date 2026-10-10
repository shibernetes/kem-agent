package agent

import (
	"context"
	"os/signal"
	"syscall"
	"testing"
)

func TestRelaySIGHUPIgnoresSignalOnceDone(t *testing.T) {
	t.Cleanup(func() { signal.Reset(syscall.SIGHUP) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	relaySIGHUP(ctx, func() {})

	// A SIGHUP that is not ignored at this point ends the process
	// while the agent stops.
	if !signal.Ignored(syscall.SIGHUP) {
		t.Error("SIGHUP is not ignored once the relay has ended")
	}
}
