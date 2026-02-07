package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

func newWorkflowsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "workflows",
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
