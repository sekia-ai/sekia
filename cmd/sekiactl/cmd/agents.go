package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

func newAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "List registered agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			var resp protocol.AgentsResponse
			if err := apiGet("/api/v1/agents", &resp); err != nil {
				return err
			}

			if len(resp.Agents) == 0 {
				fmt.Println("No agents registered.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tVERSION\tSTATUS\tEVENTS\tERRORS\tLAST HEARTBEAT")
			for _, a := range resp.Agents {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\n",
					a.Name, a.Version, a.Status,
					a.EventsProcessed, a.Errors,
					a.LastHeartbeat.Format("15:04:05"),
				)
			}
			w.Flush()
			return nil
		},
	}
}
