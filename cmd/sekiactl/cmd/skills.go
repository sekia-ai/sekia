package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/internal/skills"
)

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage skills",
	}

	cmd.AddCommand(newSkillsListCmd())

	// Default to list when no subcommand given.
	cmd.RunE = newSkillsListCmd().RunE

	return cmd
}

func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List loaded skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			var resp struct {
				Skills []skills.SkillInfo `json:"skills"`
			}
			if err := apiGet("/api/v1/skills", &resp); err != nil {
				return err
			}

			if len(resp.Skills) == 0 {
				fmt.Println("No skills loaded.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tDESCRIPTION\tTRIGGERS\tVERSION\tHANDLER")
			for _, s := range resp.Skills {
				handler := "no"
				if s.HasHandler {
					handler = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					s.Name, s.Description,
					strings.Join(s.Triggers, ", "),
					s.Version, handler,
				)
			}
			w.Flush()
			return nil
		},
	}
}
