package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sekiad status",
		RunE: func(cmd *cobra.Command, args []string) error {
			var resp protocol.StatusResponse
			if err := apiGet("/api/v1/status", &resp); err != nil {
				return err
			}

			fmt.Printf("Status:       %s\n", resp.Status)
			fmt.Printf("Uptime:       %s\n", resp.Uptime)
			fmt.Printf("NATS Running: %v\n", resp.NATSRunning)
			fmt.Printf("Started At:   %s\n", resp.StartedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("Agents:       %d\n", resp.AgentCount)
			return nil
		},
	}
}
