package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/common"
)

// newHACmd implements the `ha` shortcut commands (docs/AMENDMENTS.md unit
// brief: "ha (status = cluster health of own cluster, failover)").
// `ha status` reports the local node's own health via GET /health (which
// does not require a --cluster-id — it always describes the server the
// request lands on); `ha failover` is a thin alias for
// `cluster failover` for operators who think in terms of "this cluster"
// rather than remembering a cluster ID upfront.
func newHACmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ha",
		Short: "Quick HA status and failover for the server this CLI is pointed at",
	}
	cmd.AddCommand(newHAStatusCmd(flags))
	cmd.AddCommand(newClusterFailoverCmdAliased(flags))
	return cmd
}

func newHAStatusCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show this server's health and HA role (GET /health)",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			h, err := rc.client.Health(rc.ctx)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, h, func() ([]string, [][]string) {
				return []string{"STATUS", "DB", "HA", "ENGINE_READY", "VERSION"},
					[][]string{{h.Status, strconv.FormatBool(h.DB), strconv.FormatBool(h.HA), strconv.FormatBool(h.EngineReady), h.Version}}
			})
		},
	}
}

// newClusterFailoverCmdAliased wraps newClusterFailoverCmd under `ha` with
// the same flags/behavior, since a manual failover is scoped to a cluster
// (docs/api/endpoints.md POST /clusters/{cluster_id}/failover) regardless
// of which top-level command name the operator reaches for.
func newClusterFailoverCmdAliased(flags *persistentFlags) *cobra.Command {
	cmd := newClusterFailoverCmd(flags)
	cmd.Use = "failover <cluster-id>"
	cmd.Short = "Manually trigger a role change in a cluster (alias for 'cluster failover')"
	return cmd
}
