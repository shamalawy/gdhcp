package cli

import (
	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/common"
)

func newSearchCmd(flags *persistentFlags) *cobra.Command {
	var entityTypes []string
	var limit int

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Global search across VRFs, prefixes, scopes, pools, reservations, leases, templates, clusters, servers, and users",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			results, err := rc.client.Search(rc.ctx, args[0], entityTypes, limit)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, results, func() ([]string, [][]string) {
				headers := []string{"TYPE", "ENTITY_ID", "TITLE", "SUBTITLE", "URL"}
				rows := make([][]string, 0, len(results))
				for _, r := range results {
					rows = append(rows, []string{string(r.Type), r.EntityID, r.Title, r.Subtitle, r.URL})
				}
				return headers, rows
			})
		},
	}
	cmd.Flags().StringSliceVar(&entityTypes, "type", nil, "Restrict to these entity types (repeatable), e.g. --type scope --type pool")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results (server default if omitted)")
	return cmd
}
