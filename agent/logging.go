package agent

import (
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"

	"github.com/shibernetes/kem-agent/config"
)

// NewLogger returns the base logger every component scopes its own off.
//
// Every level goes to stderr, because the stdout sink writes event data on
// stdout and a log line landing in the middle of a batch would corrupt it.
func NewLogger(cfg config.Logging) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.Level}

	if cfg.Format == config.LogFormatText {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

// BridgeKlog routes what client-go logs through the agent's logger, so a
// reflector or informer failure comes out in the configured format rather
// than in klog's own.
func BridgeKlog(logger *slog.Logger) {
	klog.SetLogger(logr.FromSlogHandler(
		logger.With(slog.String("component", "klog")).Handler(),
	))
}
