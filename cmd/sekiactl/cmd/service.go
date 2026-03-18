package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/internal/service"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage named agent instances as background services",
		Long: `Create, start, stop, and remove named agent instances that run as
background services (launchd on macOS, systemd on Linux).

Use 'sekiactl service create' to generate a service file, then
'sekiactl service start' to run it. The default brew-managed instance
is not affected.`,
	}

	cmd.AddCommand(newServiceCreateCmd())
	cmd.AddCommand(newServiceStartCmd())
	cmd.AddCommand(newServiceStopCmd())
	cmd.AddCommand(newServiceRestartCmd())
	cmd.AddCommand(newServiceRemoveCmd())
	cmd.AddCommand(newServiceListCmd())

	// Default to list when no subcommand given.
	cmd.RunE = newServiceListCmd().RunE

	return cmd
}

func newServiceCreateCmd() *cobra.Command {
	var name string
	var configPath string
	var envVars []string

	cmd := &cobra.Command{
		Use:   "create <binary>",
		Short: "Create a background service for a named agent instance",
		Long: `Generate a service file (launchd plist on macOS, systemd unit on Linux)
for running a named agent instance in the background.

Examples:
  sekiactl service create sekia-github --name github-work
  sekiactl service create sekia-github --name github-personal --config /path/to/config.toml
  sekiactl service create sekia-slack --name slack-work --env SLACK_BOT_TOKEN=xoxb-...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			binary := args[0]

			if err := service.ValidateBinary(binary); err != nil {
				return err
			}
			if err := service.ValidateName(name); err != nil {
				return err
			}

			envMap := make(map[string]string)
			for _, kv := range envVars {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid --env format %q; expected KEY=VALUE", kv)
				}
				envMap[parts[0]] = parts[1]
			}

			if len(envMap) > 0 {
				fmt.Fprintln(os.Stderr, "Warning: environment variables in service files are stored in plaintext. Consider using encrypted config values (ENC[...]) instead.")
			}

			opts := service.CreateOpts{
				Binary:     binary,
				Name:       name,
				ConfigPath: configPath,
				EnvVars:    envMap,
			}

			if err := service.Create(opts); err != nil {
				return err
			}

			fmt.Printf("Created service %q (%s)\n", name, binary)
			fmt.Printf("  Service file: %s\n", service.ServiceFilePath(name))
			fmt.Printf("  Log file:     %s\n", service.LogPath(name))
			fmt.Printf("\nStart it with: sekiactl service start %s\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "instance name (required)")
	cmd.MarkFlagRequired("name")
	cmd.Flags().StringVar(&configPath, "config", "", "path to config file (optional)")
	cmd.Flags().StringArrayVar(&envVars, "env", nil, "environment variables as KEY=VALUE (optional, repeatable)")

	return cmd
}

func newServiceStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <name>",
		Short: "Start a named agent service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := service.Start(name); err != nil {
				return err
			}
			fmt.Printf("Started service %q\n", name)
			return nil
		},
	}
}

func newServiceStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a named agent service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := service.Stop(name); err != nil {
				return err
			}
			fmt.Printf("Stopped service %q\n", name)
			return nil
		},
	}
}

func newServiceRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <name>",
		Short: "Restart a named agent service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := service.Restart(name); err != nil {
				return err
			}
			fmt.Printf("Restarted service %q\n", name)
			return nil
		},
	}
}

func newServiceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Stop and remove a named agent service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := service.Remove(name); err != nil {
				return err
			}
			fmt.Printf("Removed service %q\n", name)
			return nil
		},
	}
}

func newServiceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed agent services",
		RunE: func(cmd *cobra.Command, args []string) error {
			services, err := service.List()
			if err != nil {
				return err
			}

			if len(services) == 0 {
				fmt.Println("No sekia services found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tBINARY\tSTATUS\tPID")
			for _, svc := range services {
				pid := "-"
				if svc.PID > 0 {
					pid = fmt.Sprintf("%d", svc.PID)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", svc.Name, svc.Binary, svc.Status, pid)
			}
			w.Flush()
			return nil
		},
	}
}
