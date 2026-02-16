package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/internal/workflow"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

func newWorkflowsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflows",
		Short: "Manage workflows",
	}

	cmd.AddCommand(newWorkflowsListCmd())
	cmd.AddCommand(newWorkflowsReloadCmd())
	cmd.AddCommand(newWorkflowsSignCmd())

	// Default to list when no subcommand given.
	cmd.RunE = newWorkflowsListCmd().RunE

	return cmd
}

func newWorkflowsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List loaded workflows",
		RunE: func(cmd *cobra.Command, args []string) error {
			var resp protocol.WorkflowsResponse
			if err := apiGet("/api/v1/workflows", &resp); err != nil {
				return err
			}

			if len(resp.Workflows) == 0 {
				fmt.Println("No workflows loaded.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tHANDLERS\tPATTERNS\tEVENTS\tERRORS\tLOADED AT")
			for _, wf := range resp.Workflows {
				fmt.Fprintf(w, "%s\t%d\t%s\t%d\t%d\t%s\n",
					wf.Name, wf.Handlers,
					strings.Join(wf.Patterns, ", "),
					wf.Events, wf.Errors,
					wf.LoadedAt.Format("15:04:05"),
				)
			}
			w.Flush()
			return nil
		},
	}
}

func newWorkflowsReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Reload all workflows from disk",
		RunE: func(cmd *cobra.Command, args []string) error {
			var resp map[string]string
			if err := apiPost("/api/v1/workflows/reload", &resp); err != nil {
				return err
			}
			fmt.Println("Workflows reloaded.")
			return nil
		},
	}
}

func newWorkflowsSignCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "sign",
		Short: "Generate SHA256 manifest for workflow files",
		Long: `Scans the workflow directory for .lua files, computes SHA256 hashes,
and writes a workflows.sha256 manifest file. This manifest is checked by the
daemon when workflows.verify_integrity is enabled.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				homeDir, _ := os.UserHomeDir()
				dir = filepath.Join(homeDir, ".config", "sekia", "workflows")
			}

			m, err := workflow.GenerateManifest(dir)
			if err != nil {
				return fmt.Errorf("generate manifest: %w", err)
			}

			if err := m.WriteFile(dir); err != nil {
				return fmt.Errorf("write manifest: %w", err)
			}

			fmt.Printf("Signed %d workflow(s) in %s\n", m.Count(), dir)
			m.WriteTo(os.Stdout)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "workflow directory (default: ~/.config/sekia/workflows)")
	return cmd
}
