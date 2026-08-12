package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shibernetes/kem-agent/internal/version"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version and exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), version.Get())
			return nil
		},
	}
}
