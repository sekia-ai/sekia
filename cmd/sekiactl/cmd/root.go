package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/pkg/sockpath"
)

var (
	socketPath string

	// Version is set by the main package via ldflags.
	Version = "dev"
)

// NewRootCmd creates the root sekiactl command.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "sekiactl",
		Short:   "sekia CLI — control the sekiad daemon",
		Version: Version,
	}

	rootCmd.PersistentFlags().StringVar(&socketPath, "socket", sockpath.DefaultSocketPath(), "sekiad Unix socket path")

	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newAgentsCmd())
	rootCmd.AddCommand(newWorkflowsCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newSecretsCmd())

	return rootCmd
}
