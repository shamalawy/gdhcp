package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newLeaseCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "lease",
		Short:   "View and manage DHCP leases",
		Aliases: []string{"leases"},
	}
	cmd.AddCommand(newLeaseListCmd(flags))
	cmd.AddCommand(newLeaseGetCmd(flags))
	cmd.AddCommand(newLeaseByIPCmd(flags))
	cmd.AddCommand(newLeaseByMACCmd(flags))
	cmd.AddCommand(newLeaseReleaseCmd(flags))
	cmd.AddCommand(newLeaseAbandonCmd(flags))
	cmd.AddCommand(newLeaseRenewCmd(flags))
	cmd.AddCommand(newLeaseDeleteCmd(flags))
	return cmd
}

// The action subcommands below accept several lease IDs and route through
// POST /leases/bulk when given more than one, so a multi-lease release is a
// single round trip and reports per-lease outcomes rather than aborting at
// the first failure.
func newLeaseActionCmd(flags *persistentFlags, use, short, action string, withDuration bool) *cobra.Command {
	var durationSeconds int64
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return runSingleLeaseAction(cmd, rc, action, args[0], durationSeconds)
			}
			resp, err := rc.client.BulkLeaseAction(rc.ctx, client.LeaseBulkRequest{
				Action: action, LeaseIDs: args, DurationSeconds: durationSeconds,
			})
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, resp, func() ([]string, [][]string) {
				headers := []string{"LEASE_ID", "RESULT"}
				rows := make([][]string, 0, len(resp.Results))
				for _, r := range resp.Results {
					result := "ok"
					if !r.OK {
						result = r.Error
					}
					rows = append(rows, []string{r.LeaseID, result})
				}
				return headers, rows
			})
		},
	}
	if withDuration {
		cmd.Flags().Int64Var(&durationSeconds, "duration", 0,
			"New lease duration in seconds (0 reuses the lease's own original duration)")
	}
	return cmd
}

func runSingleLeaseAction(cmd *cobra.Command, rc *runContext, action, id string, durationSeconds int64) error {
	switch action {
	case "release":
		msg, err := rc.client.ReleaseLease(rc.ctx, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", msg)
		return nil
	case "abandon":
		msg, err := rc.client.AbandonLease(rc.ctx, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", msg)
		return nil
	case "renew":
		l, err := rc.client.RenewLease(rc.ctx, id, durationSeconds)
		if err != nil {
			return err
		}
		return common.Render(cmd.OutOrStdout(), rc.output, l, func() ([]string, [][]string) {
			return leaseTable([]models.DHCPLease{*l})
		})
	default: // "delete"
		if err := rc.client.DeleteLease(rc.ctx, id); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "lease %s deleted\n", id)
		return nil
	}
}

func newLeaseReleaseCmd(flags *persistentFlags) *cobra.Command {
	return newLeaseActionCmd(flags, "release <lease-id> [lease-id...]",
		"Release leases so their addresses return to the pool", "release", false)
}

func newLeaseAbandonCmd(flags *persistentFlags) *cobra.Command {
	return newLeaseActionCmd(flags, "abandon <lease-id> [lease-id...]",
		"Mark leases abandoned (address answering on the wire; held back from reallocation)", "abandon", false)
}

func newLeaseRenewCmd(flags *persistentFlags) *cobra.Command {
	return newLeaseActionCmd(flags, "renew <lease-id> [lease-id...]",
		"Force-renew leases, reactivating any that had lapsed", "renew", true)
}

func newLeaseDeleteCmd(flags *persistentFlags) *cobra.Command {
	return newLeaseActionCmd(flags, "delete <lease-id> [lease-id...]",
		"Delete lease records outright", "delete", false)
}

func leaseTable(leases []models.DHCPLease) ([]string, [][]string) {
	headers := []string{"ID", "POOL_ID", "IP_ADDRESS", "CLIENT_MAC", "HOSTNAME", "STATUS", "PINNED", "LEASE_END"}
	rows := make([][]string, 0, len(leases))
	for _, l := range leases {
		mac := "-"
		if l.ClientMAC != nil {
			mac = common.MAC(l.ClientMAC)
		}
		// Render the STATUS column with AddressStatus, the operator-facing
		// status vocabulary that folded 'released' and 'expired' into 'free'
		// (amendment A20). This is what the API and web UI render, so the CLI
		// and all surfaces speak the same language.
		status := string(l.AddressStatus)
		// Render the PINNED column compactly and truthfully (amendment A19):
		// "-" for not_pinned, "yes" for pinned, "off" for pinned_disabled,
		// "->IP" for pinned_elsewhere (shows the reserved address), "other" for
		// address_reserved (a different client's reservation on this address).
		pinned := pinStateColumn(l)
		rows = append(rows, []string{l.ID, l.PoolID, common.IP(l.IPAddress), mac, common.Str(l.ClientHostname), status, pinned, common.Time(l.LeaseEnd)})
	}
	return headers, rows
}

// pinStateColumn renders a lease's reservation state for the PINNED column,
// answering the question "is this client pinned?" at a glance. Amendment A19.
func pinStateColumn(l models.DHCPLease) string {
	switch l.ReservationState {
	case models.LeaseNotPinned:
		return "-"
	case models.LeasePinned:
		return "yes"
	case models.LeasePinnedDisabled:
		return "off"
	case models.LeasePinnedElsewhere:
		// Show where this client's reservation points, so the operator knows
		// a move is needed, not a new reservation.
		if l.Reservation != nil {
			return "->" + common.IP(l.Reservation.ReservedIP)
		}
		return "elsewhere"
	case models.LeaseAddressReserved:
		return "other"
	default:
		return "?"
	}
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
