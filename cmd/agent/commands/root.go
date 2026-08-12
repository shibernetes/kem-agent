package commands

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/shibernetes/kem-agent/cmd/agent/commands/validate"
)

// NewRootCommand builds the agent root command.
func NewRootCommand(run func(ctx context.Context, configPath, kubeconfig string) error) *cobra.Command {
	var (
		configPath string
		kubeconfig string
	)
	cmd := &cobra.Command{
		Use:           filepath.Base(os.Args[0]),
		Short:         "Kubernetes Events collection and forwarding agent",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), configPath, kubeconfig)
		},
	}
	cmd.CompletionOptions.DisableDefaultCmd = true

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "path to the agent config file")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to a kubeconfig file, for running outside a cluster")

	bindKlogFlags(cmd)

	_ = cmd.MarkFlagRequired("config")
	cmd.AddCommand(validate.NewCommand(), newVersionCommand())

	return cmd
}

// bindKlogFlags exposes klog's -v and -vmodule verbosity flags on cmd.
// Note that klog logs through slog, so its verbosity only takes effect
// when the log level is set to 'debug'.
func bindKlogFlags(cmd *cobra.Command) {
	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)

	for _, name := range []string{
		"v", "vmodule",
	} {
		cmd.Flags().AddGoFlag(fs.Lookup(name))
	}
}
