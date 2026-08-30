package cli

import (
	"encoding/hex"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newPrefixCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "prefix",
		Short:   "Manage IPAM prefixes (aggregates, containers, and subnets)",
		Aliases: []string{"prefixes"},
	}
	cmd.AddCommand(newPrefixListCmd(flags))
	cmd.AddCommand(newPrefixGetCmd(flags))
	cmd.AddCommand(newPrefixCreateCmd(flags))
	cmd.AddCommand(newPrefixDeleteCmd(flags))
	cmd.AddCommand(newPrefixClientsCmd(flags))
	return cmd
}

func prefixTable(prefixes []models.IPAMPrefix) ([]string, [][]string) {
	headers := []string{"ID", "VRF_ID", "CIDR", "TYPE", "NAME", "UTIL%", "DESCRIPTION"}
	rows := make([][]string, 0, len(prefixes))
	for _, p := range prefixes {
		util := "-"
		if p.UtilizationPercentage != nil {
			util = fmt.Sprintf("%d", *p.UtilizationPercentage)
		}
		rows = append(rows, []string{p.ID, p.VRFID, common.CIDR(p.Prefix), string(p.PrefixType), common.Str(p.Name), util, common.Str(p.Description)})
	}
	return headers, rows
}

func newPrefixListCmd(flags *persistentFlags) *cobra.Command {
	var vrfID, prefixType string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List IPAM prefixes",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListPrefixes(rc.ctx, common.ListParams(cmd), vrfID, prefixType)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return prefixTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	cmd.Flags().StringVar(&vrfID, "vrf-id", "", "Filter by VRF ID")
	cmd.Flags().StringVar(&prefixType, "type", "", "Filter by type: aggregate|container|subnet")
	return cmd
}

func newPrefixGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <prefix-id>",
		Short: "Get a single IPAM prefix",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			p, err := rc.client.GetPrefix(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, p, func() ([]string, [][]string) { return prefixTable([]models.IPAMPrefix{*p}) })
		},
	}
}

func newPrefixCreateCmd(flags *persistentFlags) *cobra.Command {
	var vrfID, cidr, prefixType, name, description string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an IPAM prefix",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.PrefixCreateRequest{VRFID: vrfID, CIDR: cidr, Type: prefixType}
			if name != "" {
				req.Name = &name
			}
			if description != "" {
				req.Description = &description
			}
			p, err := rc.client.CreatePrefix(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, p, func() ([]string, [][]string) { return prefixTable([]models.IPAMPrefix{*p}) })
		},
	}
	cmd.Flags().StringVar(&vrfID, "vrf-id", "", "VRF ID (required)")
	cmd.Flags().StringVar(&cidr, "cidr", "", "CIDR, e.g. 10.1.0.0/24 (required)")
	cmd.Flags().StringVar(&prefixType, "type", "", "aggregate|container|subnet (required)")
	cmd.Flags().StringVar(&name, "name", "", "Name")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.MarkFlagRequired("vrf-id")
	cmd.MarkFlagRequired("cidr")
	cmd.MarkFlagRequired("type")
	return cmd
}

func newPrefixDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <prefix-id>",
		Short: "Delete an IPAM prefix (must have no children)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeletePrefix(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "prefix %s deleted\n", args[0])
			return nil
		},
	}
}

func newPrefixClientsCmd(flags *persistentFlags) *cobra.Command {
	var kind, state string
	cmd := &cobra.Command{
		Use:   "clients <prefix-id>",
		Short: "List DHCP clients (leases and reservations) in a prefix and its descendants",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListPrefixClients(rc.ctx, args[0], common.ListParams(cmd), kind, state)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) {
				headers := []string{"KIND", "IP_ADDRESS", "SCOPE", "POOL", "CLIENT_MAC", "CLIENT_ID", "HOSTNAME", "STATE", "EXPIRES", "IDENTIFIER"}
				rows := make([][]string, 0, len(page.Items))
				for _, c := range page.Items {
					mac := "-"
					if c.ClientMAC != nil {
						mac = c.ClientMAC.String()
					}
					clientID := "-"
					if len(c.ClientID) > 0 {
						clientID = hex.EncodeToString(c.ClientID)
					}
					hostname := "-"
					if c.ClientHostname != nil {
						hostname = *c.ClientHostname
					}
					// A lease carries a binding state; a reservation instead
					// carries the identifier it is bound to.
					bindingState := "-"
					if c.BindingState != nil {
						bindingState = string(*c.BindingState)
					}
					expires := "-"
					if c.LeaseEnd != nil {
						expires = common.Time(*c.LeaseEnd)
					}
					identifier := "-"
					if c.BindingKey != nil {
						identifier = *c.BindingKey
						if c.IsEnabled != nil && !*c.IsEnabled {
							identifier += " (disabled)"
						}
					}
					rows = append(rows, []string{
						string(c.Kind),
						common.IP(c.IPAddress),
						c.ScopeName,
						c.PoolName,
						mac,
						clientID,
						hostname,
						bindingState,
						expires,
						identifier,
					})
				}
				return headers, rows
			})
		},
	}
	common.AddListFlags(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by kind: lease|reservation")
	cmd.Flags().StringVar(&state, "state", "", "Filter by binding state (leases only): active|expired|released|abandoned")
	return cmd
}

func newTreeCmd(flags *persistentFlags) *cobra.Command {
	var vrfID, parentID string
	var depth int

	cmd := &cobra.Command{
		Use:   "tree",
		Short: "Show the IPAM prefix hierarchy as a tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			nodes, err := rc.client.IPAMTree(rc.ctx, vrfID, parentID, depth)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, nodes, func() ([]string, [][]string) {
				headers := []string{"PREFIX_ID", "CIDR", "TYPE", "NAME", "CHILDREN", "HAS_SCOPE"}
				var rows [][]string
				var walk func(prefix string, nodes []*models.IPAMTreeNode)
				walk = func(prefix string, nodes []*models.IPAMTreeNode) {
					for _, n := range nodes {
						if n.Prefix == nil {
							continue
						}
						rows = append(rows, []string{
							n.Prefix.ID, prefix + common.CIDR(n.Prefix.Prefix), string(n.Prefix.PrefixType),
							common.Str(n.Prefix.Name), fmt.Sprintf("%d", n.ChildCount), common.Bool(n.HasScope),
						})
						walk(prefix+"  ", n.Children)
					}
				}
				walk("", nodes)
				return headers, rows
			})
		},
	}
	cmd.Flags().StringVar(&vrfID, "vrf-id", "", "VRF ID (required by the server for a rooted tree)")
	cmd.Flags().StringVar(&parentID, "parent-id", "", "Root the tree at this prefix instead of the VRF root")
	cmd.Flags().IntVar(&depth, "depth", 0, "How many levels to expand (0 = server default)")
	return cmd
}
