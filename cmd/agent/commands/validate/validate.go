package validate

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/shibernetes/kem-agent/cmd/internal/sinks"
)

// ErrInvalidConfig signals aqn invalid configuration.
var ErrInvalidConfig = errors.New("invalid configuration")

// NewCommand builds the config validation subcommand.
func NewCommand() *cobra.Command {
	var (
		configPath string
		output     string
		color      string
	)
	cmd := &cobra.Command{
		Use:   "validate-config",
		Short: "Validate a configuration file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.OutOrStdout(), configPath, output, color)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "config file path, or stdin when unset")
	cmd.Flags().StringVar(&output, "output", outputText, "output format (text, json)")
	cmd.Flags().StringVar(&color, "color", colorAuto, "whether to colorize the output (auto, always, off)")

	return cmd
}

// readConfig reads the configuration from a path, or from stdin when its empty.
func readConfig(path string) ([]byte, string, error) {
	if path == "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read stdin: %w", err)
		}
		return b, "<stdin>", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read config file %q: %w", path, err)
	}
	return b, path, nil
}

func run(w io.Writer, configPath, output, color string) error {
	if err := validateOutputFlag(output); err != nil {
		return err
	}
	if err := validateColorFlag(color); err != nil {
		return err
	}
	b, path, err := readConfig(configPath)
	if err != nil {
		return err
	}
	res := analyze(path, b, sinks.Factories())

	if err := render(w, &res, output, color); err != nil {
		return err
	}
	if !res.isValid() {
		return ErrInvalidConfig
	}
	return nil
}
