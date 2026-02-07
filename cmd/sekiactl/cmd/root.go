package cmd

import "github.com/spf13/cobra"

var (
	socketPath string

	// Version is set by the main package via ldflags.
	Version = "dev"
)

// NewRootCmd creates the root sekiactl command.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "sekiactl",
		Short:   "Sekia CLI — control the sekiad daemon",
		Version: Version,
	}

	rootCmd.PersistentFlags().StringVar(&socketPath, "socket", "/tmp/sekiad.sock", "sekiad Unix socket path")

	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newAgentsCmd())
	rootCmd.AddCommand(newWorkflowsCmd())

	return rootCmd
}
