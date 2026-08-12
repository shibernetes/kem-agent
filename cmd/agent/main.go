package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"

	"github.com/KimMachineGun/automemlimit/memlimit"

	"github.com/shibernetes/kem-agent/agent"
	"github.com/shibernetes/kem-agent/cmd/agent/commands"
	"github.com/shibernetes/kem-agent/cmd/agent/commands/validate"
	"github.com/shibernetes/kem-agent/cmd/internal/sinks"
	"github.com/shibernetes/kem-agent/config"
	"github.com/shibernetes/kem-agent/internal/kube"
	"github.com/shibernetes/kem-agent/sink"
)

func main() {
	err := commands.NewRootCommand(run).Execute()

	// Exit 1 for a bad config or a fatal agent error, both already
	// reported, and 2 for a usage or tooling error.
	switch {
	case err == nil:
		os.Exit(0)
	case errors.Is(err, errQuit), errors.Is(err, validate.ErrInvalidConfig):
		os.Exit(1)
	default:
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

// errQuit signals a failure that was already reported, so the main function exits without printing it again.
var errQuit = errors.New("already reported")

func run(ctx context.Context, configPath, kubeconfig string) error {
	factories := sinks.Factories()

	logger := agent.NewLogger(config.DefaultServiceConfig().Logging)
	agent.BridgeKlog(logger)

	res, err := loadConfig(ctx, logger, configPath, factories)
	if err != nil {
		return err
	}
	logger = agent.NewLogger(res.Config.Service.Logging)
	agent.BridgeKlog(logger)
	setMemoryLimit(ctx, logger)

	for _, warning := range agent.ConfigWarnings(res) {
		attrs := agent.ConfigWarningAttrs(configPath, warning)
		logger.LogAttrs(ctx, slog.LevelWarn, warning.Message, attrs...)
	}
	kubeConfig, err := kube.Load(kubeconfig)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "failed to load Kubernetes client configuration",
			slog.Any("error", err),
		)
		return errQuit
	}
	a, err := agent.New(res.Config, agent.Options{
		Factories:  factories,
		Logger:     logger,
		ConfigPath: configPath,
		Kube:       kubeConfig,
	})
	if err != nil {
		attrs := agent.ConfigFailureAttrs(res, configPath, err)
		logger.LogAttrs(ctx, slog.LevelError, "failed to build agent", attrs...)
		return errQuit
	}
	if err := a.Run(ctx); err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "agent terminated", slog.Any("error", err))
		return errQuit
	}
	return nil
}

// loadConfig reads and parses the configuration file.
func loadConfig(ctx context.Context, logger *slog.Logger, path string, factories sink.Factories) (*config.Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "failed to read config file",
			slog.Any("error", err),
			slog.String("file", path),
		)
		return nil, errQuit
	}
	res, err := config.Parse(data, factories)
	if err != nil {
		// A config that fails validation is still returned with
		// its warnings, which are reported before the error.
		if res != nil {
			for _, warning := range agent.ConfigWarnings(res) {
				logger.LogAttrs(ctx, slog.LevelWarn, warning.Message,
					agent.ConfigWarningAttrs(path, warning)...)
			}
		}
		logger.LogAttrs(ctx, slog.LevelError, "failed to parse configuration",
			agent.ConfigFailureAttrs(nil, path, err)...)
		return nil, errQuit
	}
	return res, nil
}

func setMemoryLimit(ctx context.Context, logger *slog.Logger) {
	limit, err := memlimit.Set()

	switch {
	case err != nil:
		logger.LogAttrs(ctx, slog.LevelWarn, "memory limit could not be determined",
			slog.Any("error", err),
		)
	case limit == math.MaxInt64:
		logger.LogAttrs(ctx, slog.LevelInfo, "memory is not limited")
	default:
		logger.LogAttrs(ctx, slog.LevelInfo, "memory limit set",
			slog.Int64("limitBytes", limit),
		)
	}
}
