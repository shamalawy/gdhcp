package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newScopeCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scope",
		Short:   "Manage DHCP scopes (a subnet served by DHCP, bound to a VRF/cluster/IPAM prefix)",
		Aliases: []string{"scopes"},
	}
	cmd.AddCommand(newScopeListCmd(flags))
	cmd.AddCommand(newScopeGetCmd(flags))
	cmd.AddCommand(newScopeCreateCmd(flags))
	cmd.AddCommand(newScopeUpdateCmd(flags))
	cmd.AddCommand(newScopeDeleteCmd(flags))
	cmd.AddCommand(newScopeUtilizationCmd(flags))
	cmd.AddCommand(newScopeStatsCmd(flags))
	cmd.AddCommand(newScopeOptionsCmd(flags))
	cmd.AddCommand(newScopeTemplateCmd(flags))
	return cmd
}

func scopeTable(scopes []models.DHCPScope) ([]string, [][]string) {
	headers := []string{"ID", "NAME", "VRF_ID", "CLUSTER_ID", "NETWORK", "ENABLED", "DESCRIPTION"}
	rows := make([][]string, 0, len(scopes))
	for _, s := range scopes {
		rows = append(rows, []string{s.ID, s.Name, s.VRFID, s.ClusterID, common.CIDR(s.Network), common.Bool(s.IsEnabled), common.Str(s.Description)})
	}
	return headers, rows
}

func newScopeListCmd(flags *persistentFlags) *cobra.Command {
	var vrfID, clusterID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List DHCP scopes",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListScopes(rc.ctx, common.ListParams(cmd), vrfID, clusterID)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return scopeTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	cmd.Flags().StringVar(&vrfID, "vrf-id", "", "Filter by VRF ID")
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "Filter by cluster ID")
	return cmd
}

func newScopeGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <scope-id>",
		Short: "Get a single DHCP scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			s, err := rc.client.GetScope(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, s, func() ([]string, [][]string) { return scopeTable([]models.DHCPScope{*s}) })
		},
	}
}

func newScopeCreateCmd(flags *persistentFlags) *cobra.Command {
	var vrfID, clusterID, ipamPrefixID, name, description string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a DHCP scope",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.ScopeCreateRequest{VRFID: vrfID, ClusterID: clusterID, IPAMPrefixID: ipamPrefixID, Name: name}
			if description != "" {
				req.Description = &description
			}
			s, err := rc.client.CreateScope(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, s, func() ([]string, [][]string) { return scopeTable([]models.DHCPScope{*s}) })
		},
	}
	cmd.Flags().StringVar(&vrfID, "vrf-id", "", "VRF ID (required)")
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "Cluster ID (required)")
	cmd.Flags().StringVar(&ipamPrefixID, "ipam-prefix-id", "", "IPAM subnet prefix ID (required)")
	cmd.Flags().StringVar(&name, "name", "", "Scope name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.MarkFlagRequired("vrf-id")
	cmd.MarkFlagRequired("cluster-id")
	cmd.MarkFlagRequired("ipam-prefix-id")
	cmd.MarkFlagRequired("name")
	return cmd
}

func newScopeUpdateCmd(flags *persistentFlags) *cobra.Command {
	var name, description string
	var enabled bool
	cmd := &cobra.Command{
		Use:   "update <scope-id>",
		Short: "Update a DHCP scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.ScopeUpdateRequest{
				Name:        common.StringFlagPtr(cmd, "name"),
				Description: common.StringFlagPtr(cmd, "description"),
				IsEnabled:   common.BoolFlagPtr(cmd, "enabled"),
			}
			s, err := rc.client.UpdateScope(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, s, func() ([]string, [][]string) { return scopeTable([]models.DHCPScope{*s}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "Enable or disable the scope")
	return cmd
}

func newScopeDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <scope-id>",
		Short: "Delete a DHCP scope (cascades to its pools)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeleteScope(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "scope %s deleted\n", args[0])
			return nil
		},
	}
}

func newScopeUtilizationCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "utilization <scope-id>",
		Short: "Show current utilization for a scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			u, err := rc.client.ScopeUtilization(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, u, func() ([]string, [][]string) { return utilizationTable(u) })
		},
	}
}

func newScopeStatsCmd(flags *persistentFlags) *cobra.Command {
	var window string
	cmd := &cobra.Command{
		Use:   "stats <scope-id>",
		Short: "Show DHCP transaction stats for a scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			s, err := rc.client.ScopeStats(rc.ctx, args[0], window)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, s, func() ([]string, [][]string) { return dhcpStatsTable(s) })
		},
	}
	cmd.Flags().StringVar(&window, "window", "", "Time window, e.g. 24h, 7d (server default if omitted)")
	return cmd
}

func utilizationTable(u *models.Utilization) ([]string, [][]string) {
	headers := []string{"ENTITY_TYPE", "ENTITY_ID", "TOTAL", "ACTIVE", "RESERVED", "AVAILABLE", "PCT"}
	row := []string{string(u.EntityType), u.EntityID, fmt.Sprintf("%d", u.Total), fmt.Sprintf("%d", u.Active), fmt.Sprintf("%d", u.Reserved), fmt.Sprintf("%d", u.Available), fmt.Sprintf("%.1f%%", u.UtilizationPercentage)}
	return headers, [][]string{row}
}

func dhcpStatsTable(s *models.DHCPStats) ([]string, [][]string) {
	headers := []string{"SCOPE_ID", "DISCOVER", "OFFER", "REQUEST", "ACK", "NAK", "DECLINE", "RELEASE", "NO_LEASES"}
	row := []string{s.ScopeID, fmt.Sprintf("%d", s.DiscoverCount), fmt.Sprintf("%d", s.OfferCount), fmt.Sprintf("%d", s.RequestCount), fmt.Sprintf("%d", s.AckCount), fmt.Sprintf("%d", s.NakCount), fmt.Sprintf("%d", s.DeclineCount), fmt.Sprintf("%d", s.ReleaseCount), fmt.Sprintf("%d", s.NoLeasesCount)}
	return headers, [][]string{row}
}

// optionValueTable renders []*models.OptionValue for any of the
// scope/pool/template "get options" commands.
func optionValueTable(opts []*models.OptionValue) ([]string, [][]string) {
	headers := []string{"ID", "CODE", "NAME", "VALUE", "ENABLED"}
	rows := make([][]string, 0, len(opts))
	for _, o := range opts {
		name := "-"
		if o.Catalog != nil {
			name = o.Catalog.Name
		}
		rows = append(rows, []string{o.ID, fmt.Sprintf("%d", o.OptionCode), name, o.Value, common.Bool(o.IsEnabled)})
	}
	return headers, rows
}

func newScopeOptionsCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Manage DHCP option values set on a scope",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "get <scope-id>",
		Short: "List option values set on a scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			opts, err := rc.client.GetScopeOptions(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, opts, func() ([]string, [][]string) { return optionValueTable(opts) })
		},
	})

	var value string
	var code int
	setCmd := &cobra.Command{
		Use:   "set <scope-id>",
		Short: "Set (create) an option value on a scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			ov, err := rc.client.SetScopeOption(rc.ctx, args[0], int16(code), value)
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

	var newValue string
	var enabled bool
	updateCmd := &cobra.Command{
		Use:   "update <scope-id> <option-id>",
		Short: "Update an option value's value/enabled state on a scope",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.OptionValueUpdateRequest{
				Value:     common.StringFlagPtr(cmd, "value"),
				IsEnabled: common.BoolFlagPtr(cmd, "enabled"),
			}
			ov, err := rc.client.UpdateScopeOption(rc.ctx, args[0], args[1], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, ov, func() ([]string, [][]string) { return optionValueTable([]*models.OptionValue{ov}) })
		},
	}
	updateCmd.Flags().StringVar(&newValue, "value", "", "New value")
	updateCmd.Flags().BoolVar(&enabled, "enabled", true, "Enable or disable")
	cmd.AddCommand(updateCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "unset <scope-id> <option-id>",
		Short: "Remove an option value from a scope",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.UnsetScopeOption(rc.ctx, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "option %s removed from scope %s\n", args[1], args[0])
			return nil
		},
	})

	return cmd
}

func newScopeTemplateCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Attach or detach a template from a scope",
	}

	var priority int
	attachCmd := &cobra.Command{
		Use:   "attach <scope-id> <template-id>",
		Short: "Attach a template to a scope",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			assoc, err := rc.client.AttachTemplate(rc.ctx, args[1], args[0], priority)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, assoc, func() ([]string, [][]string) {
				return []string{"ID", "TEMPLATE_ID", "SCOPE_ID", "PRIORITY"}, [][]string{{assoc.ID, assoc.TemplateID, assoc.ScopeID, fmt.Sprintf("%d", assoc.Priority)}}
			})
		},
	}
	attachCmd.Flags().IntVar(&priority, "priority", 0, "Priority (higher checked first in option inheritance)")
	cmd.AddCommand(attachCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "detach <scope-id> <template-id>",
		Short: "Detach a template from a scope",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DetachTemplate(rc.ctx, args[1], args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "template %s detached from scope %s\n", args[1], args[0])
			return nil
		},
	})

	return cmd
}
