package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newPoolCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pool",
		Short:   "Manage DHCP pools (contiguous allocatable IP ranges within a scope)",
		Aliases: []string{"pools"},
	}
	cmd.AddCommand(newPoolListCmd(flags))
	cmd.AddCommand(newPoolGetCmd(flags))
	cmd.AddCommand(newPoolCreateCmd(flags))
	cmd.AddCommand(newPoolUpdateCmd(flags))
	cmd.AddCommand(newPoolDeleteCmd(flags))
	cmd.AddCommand(newPoolUtilizationCmd(flags))
	cmd.AddCommand(newPoolOptionsCmd(flags))
	cmd.AddCommand(newPoolReservationCmd(flags))
	cmd.AddCommand(newPoolLeasesCmd(flags))
	return cmd
}

func poolTable(pools []models.DHCPPool) ([]string, [][]string) {
	headers := []string{"ID", "SCOPE_ID", "NAME", "RANGE_START", "RANGE_END", "PRIORITY", "ENABLED"}
	rows := make([][]string, 0, len(pools))
	for _, p := range pools {
		rows = append(rows, []string{p.ID, p.ScopeID, p.Name, common.IP(p.RangeStart), common.IP(p.RangeEnd), fmt.Sprintf("%d", p.Priority), common.Bool(p.IsEnabled)})
	}
	return headers, rows
}

func newPoolListCmd(flags *persistentFlags) *cobra.Command {
	var scopeID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List DHCP pools in a scope",
		RunE: func(cmd *cobra.Command, args []string) error {
			if scopeID == "" {
				return fmt.Errorf("pool list: --scope-id is required")
			}
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListPools(rc.ctx, scopeID, common.ListParams(cmd))
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return poolTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	cmd.Flags().StringVar(&scopeID, "scope-id", "", "Scope ID to list pools under (required)")
	cmd.MarkFlagRequired("scope-id")
	return cmd
}

func newPoolGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <pool-id>",
		Short: "Get a single DHCP pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			p, err := rc.client.GetPool(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, p, func() ([]string, [][]string) { return poolTable([]models.DHCPPool{*p}) })
		},
	}
}

func newPoolCreateCmd(flags *persistentFlags) *cobra.Command {
	var scopeID, name, rangeStart, rangeEnd string
	var priority int
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a DHCP pool under a scope",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.PoolCreateRequest{Name: name, RangeStart: rangeStart, RangeEnd: rangeEnd, Priority: priority}
			p, err := rc.client.CreatePool(rc.ctx, scopeID, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, p, func() ([]string, [][]string) { return poolTable([]models.DHCPPool{*p}) })
		},
	}
	cmd.Flags().StringVar(&scopeID, "scope-id", "", "Scope ID (required)")
	cmd.Flags().StringVar(&name, "name", "", "Pool name (required)")
	cmd.Flags().StringVar(&rangeStart, "range-start", "", "Range start IP (required)")
	cmd.Flags().StringVar(&rangeEnd, "range-end", "", "Range end IP (required)")
	cmd.Flags().IntVar(&priority, "priority", 0, "Allocation priority (higher = preferred)")
	cmd.MarkFlagRequired("scope-id")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("range-start")
	cmd.MarkFlagRequired("range-end")
	return cmd
}

func newPoolUpdateCmd(flags *persistentFlags) *cobra.Command {
	var name, rangeStart, rangeEnd string
	var priority int
	var enabled bool
	cmd := &cobra.Command{
		Use:   "update <pool-id>",
		Short: "Update a DHCP pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.PoolUpdateRequest{
				Name:       common.StringFlagPtr(cmd, "name"),
				RangeStart: common.StringFlagPtr(cmd, "range-start"),
				RangeEnd:   common.StringFlagPtr(cmd, "range-end"),
				Priority:   common.IntFlagPtr(cmd, "priority"),
				IsEnabled:  common.BoolFlagPtr(cmd, "enabled"),
			}
			p, err := rc.client.UpdatePool(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, p, func() ([]string, [][]string) { return poolTable([]models.DHCPPool{*p}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name")
	cmd.Flags().StringVar(&rangeStart, "range-start", "", "New range start IP")
	cmd.Flags().StringVar(&rangeEnd, "range-end", "", "New range end IP")
	cmd.Flags().IntVar(&priority, "priority", 0, "New priority")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "Enable or disable the pool")
	return cmd
}

func newPoolDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <pool-id>",
		Short: "Delete a DHCP pool (cascades to its leases)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeletePool(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pool %s deleted\n", args[0])
			return nil
		},
	}
}

func newPoolUtilizationCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "utilization <pool-id>",
		Short: "Show current utilization for a pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			u, err := rc.client.PoolUtilization(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, u, func() ([]string, [][]string) { return utilizationTable(u) })
		},
	}
}

func newPoolOptionsCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Manage DHCP option values set on a pool",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "get <pool-id>",
		Short: "List option values set on a pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			opts, err := rc.client.GetPoolOptions(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, opts, func() ([]string, [][]string) { return optionValueTable(opts) })
		},
	})

	var value string
	var code int
	setCmd := &cobra.Command{
		Use:   "set <pool-id>",
		Short: "Set (create) an option value on a pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			ov, err := rc.client.SetPoolOption(rc.ctx, args[0], int16(code), value)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, ov, func() ([]string, [][]string) { return optionValueTable([]*models.OptionValue{ov}) })
		},
	}
	setCmd.Flags().IntVar(&code, "code", 0, "DHCP option code (required)")
	setCmd.Flags().StringVar(&value, "value", "", "Option value (required)")
	setCmd.MarkFlagRequired("code")
	setCmd.MarkFlagRequired("value")
	cmd.AddCommand(setCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "unset <pool-id> <option-id>",
		Short: "Remove an option value from a pool",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.UnsetPoolOption(rc.ctx, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "option %s removed from pool %s\n", args[1], args[0])
			return nil
		},
	})

	return cmd
}

func newPoolLeasesCmd(flags *persistentFlags) *cobra.Command {
	var state string
	cmd := &cobra.Command{
		Use:   "leases <pool-id>",
		Short: "List leases in a pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.PoolLeases(rc.ctx, args[0], common.ListParams(cmd), state)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return leaseTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	cmd.Flags().StringVar(&state, "state", "", "Filter by binding state: active|expired|released|abandoned")
	return cmd
}

// -- Reservations (addressed via pool per product rule, docs/AMENDMENTS.md A2) --

func newPoolReservationCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "reservation",
		Short:   "Manage DHCP reservations (fixed IP-to-client bindings within a pool)",
		Aliases: []string{"reservations"},
	}
	cmd.AddCommand(newReservationListCmd(flags))
	cmd.AddCommand(newReservationGetCmd(flags))
	cmd.AddCommand(newReservationAddCmd(flags))
	cmd.AddCommand(newReservationUpdateCmd(flags))
	cmd.AddCommand(newReservationRemoveCmd(flags))
	return cmd
}

func reservationTable(reservations []models.DHCPReservation) ([]string, [][]string) {
	headers := []string{"ID", "POOL_ID", "RESERVED_IP", "BINDING_TYPE", "BINDING_KEY", "HOSTNAME", "ENABLED"}
	rows := make([][]string, 0, len(reservations))
	for _, r := range reservations {
		rows = append(rows, []string{r.ID, r.PoolID, common.IP(r.ReservedIP), string(r.BindingType), r.BindingKey, common.Str(r.Hostname), common.Bool(r.IsEnabled)})
	}
	return headers, rows
}

func newReservationListCmd(flags *persistentFlags) *cobra.Command {
	var poolID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List reservations in a pool",
		RunE: func(cmd *cobra.Command, args []string) error {
			if poolID == "" {
				return fmt.Errorf("reservation list: --pool-id is required")
			}
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListReservations(rc.ctx, poolID, common.ListParams(cmd))
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return reservationTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	cmd.Flags().StringVar(&poolID, "pool-id", "", "Pool ID to list reservations under (required)")
	cmd.MarkFlagRequired("pool-id")
	return cmd
}

func newReservationGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <reservation-id>",
		Short: "Get a single reservation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			r, err := rc.client.GetReservation(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, r, func() ([]string, [][]string) { return reservationTable([]models.DHCPReservation{*r}) })
		},
	}
}

func newReservationAddCmd(flags *persistentFlags) *cobra.Command {
	var poolID, bindingKey, bindingType, reservedIP, hostname, description string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a reservation to a pool",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.ReservationCreateRequest{BindingKey: bindingKey, BindingType: bindingType, ReservedIP: reservedIP}
			if hostname != "" {
				req.Hostname = &hostname
			}
			if description != "" {
				req.Description = &description
			}
			r, err := rc.client.AddReservation(rc.ctx, poolID, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, r, func() ([]string, [][]string) { return reservationTable([]models.DHCPReservation{*r}) })
		},
	}
	cmd.Flags().StringVar(&poolID, "pool-id", "", "Pool ID (required)")
	cmd.Flags().StringVar(&bindingKey, "binding-key", "", "Client identity, e.g. aa:bb:cc:dd:ee:ff for MAC or hex string for other types (required)")
	cmd.Flags().StringVar(&bindingType, "binding-type", "mac", "mac|client_id|hostname|circuit_id|remote_id|vendor_class|user_class")
	cmd.Flags().StringVar(&reservedIP, "reserved-ip", "", "IP to reserve, must be inside the pool range (required)")
	cmd.Flags().StringVar(&hostname, "hostname", "", "Hostname")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.MarkFlagRequired("pool-id")
	cmd.MarkFlagRequired("binding-key")
	cmd.MarkFlagRequired("reserved-ip")
	cmd.MarkFlagRequired("binding-type")
	return cmd
}

func newReservationUpdateCmd(flags *persistentFlags) *cobra.Command {
	var hostname, description, bindingKey, bindingType string
	var enabled bool
	cmd := &cobra.Command{
		Use:   "update <reservation-id>",
		Short: "Update a reservation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.ReservationUpdateRequest{
				Hostname:    common.StringFlagPtr(cmd, "hostname"),
				Description: common.StringFlagPtr(cmd, "description"),
				IsEnabled:   common.BoolFlagPtr(cmd, "enabled"),
				BindingKey:  common.StringFlagPtr(cmd, "binding-key"),
				BindingType: common.StringFlagPtr(cmd, "binding-type"),
			}
			r, err := rc.client.UpdateReservation(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, r, func() ([]string, [][]string) { return reservationTable([]models.DHCPReservation{*r}) })
		},
	}
	cmd.Flags().StringVar(&hostname, "hostname", "", "New hostname")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().StringVar(&bindingKey, "binding-key", "", "New binding key (raw value; type prefix is added automatically)")
	cmd.Flags().StringVar(&bindingType, "binding-type", "", "mac|client_id|hostname|circuit_id|remote_id|vendor_class|user_class")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "Enable or disable the reservation")
	return cmd
}

func newReservationRemoveCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <reservation-id>",
		Short: "Remove a reservation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.RemoveReservation(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "reservation %s removed\n", args[0])
			return nil
		},
	}
}
