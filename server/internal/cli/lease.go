package cli

import (
	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newLeaseCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "lease",
		Short:   "View DHCP leases (read-only; leases are managed internally by the DHCP engine)",
		Aliases: []string{"leases"},
	}
	cmd.AddCommand(newLeaseListCmd(flags))
	cmd.AddCommand(newLeaseGetCmd(flags))
	cmd.AddCommand(newLeaseByIPCmd(flags))
	cmd.AddCommand(newLeaseByMACCmd(flags))
	return cmd
}

func leaseTable(leases []models.DHCPLease) ([]string, [][]string) {
	headers := []string{"ID", "POOL_ID", "IP_ADDRESS", "CLIENT_MAC", "HOSTNAME", "STATE", "LEASE_END"}
	rows := make([][]string, 0, len(leases))
	for _, l := range leases {
		mac := "-"
		if l.ClientMAC != nil {
			mac = common.MAC(l.ClientMAC)
		}
		rows = append(rows, []string{l.ID, l.PoolID, common.IP(l.IPAddress), mac, common.Str(l.ClientHostname), string(l.BindingState), common.Time(l.LeaseEnd)})
	}
	return headers, rows
}

func newLeaseListCmd(flags *persistentFlags) *cobra.Command {
	var scopeID, poolID, state string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all leases (filterable by scope/pool/state)",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListLeases(rc.ctx, common.ListParams(cmd), scopeID, poolID, state)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return leaseTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	cmd.Flags().StringVar(&scopeID, "scope-id", "", "Filter by scope ID")
	cmd.Flags().StringVar(&poolID, "pool-id", "", "Filter by pool ID")
	cmd.Flags().StringVar(&state, "state", "", "Filter by binding state: active|expired|released|abandoned")
	return cmd
}

func newLeaseGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <lease-id>",
		Short: "Get a single lease",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			l, err := rc.client.GetLease(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, l, func() ([]string, [][]string) { return leaseTable([]models.DHCPLease{*l}) })
		},
	}
}

func newLeaseByIPCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "by-ip <ip-address>",
		Short: "Get the lease for an IP address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			l, err := rc.client.GetLeaseByIP(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, l, func() ([]string, [][]string) { return leaseTable([]models.DHCPLease{*l}) })
		},
	}
}

func newLeaseByMACCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "by-mac <mac-address>",
		Short: "Get leases for a MAC address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			leases, err := rc.client.GetLeasesByMAC(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, leases, func() ([]string, [][]string) {
				deref := make([]models.DHCPLease, len(leases))
				for i, l := range leases {
					deref[i] = *l
				}
				return leaseTable(deref)
			})
		},
	}
}
