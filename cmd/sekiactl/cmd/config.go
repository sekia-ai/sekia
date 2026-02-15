package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage daemon and agent configuration",
	}

	cmd.AddCommand(newConfigReloadCmd())

	return cmd
}

func newConfigReloadCmd() *cobra.Command {
	var target string

	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Signal agents and/or daemon to reload their configuration",
		Long: `Sends a config reload signal via NATS. By default, broadcasts to all
agents and the daemon. Use --target to reload a specific component.

Examples:
  sekiactl config reload                  # reload all
  sekiactl config reload --target sekiad  # reload daemon only
  sekiactl config reload --target github-agent`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/v1/config/reload"
			if target != "" {
				path += "?target=" + target
			}

			var resp protocol.ConfigReloadResponse
			if err := apiPost(path, &resp); err != nil {
				return err
			}
			fmt.Printf("Config reload requested for: %s\n", resp.Target)
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "specific agent or 'sekiad' (default: all)")

	return cmd
}
