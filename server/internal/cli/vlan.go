package cli

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newVLANCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "vlan",
		Short:   "Manage VLANs and VLAN groups (layer-2 segments assigned to IPAM prefixes)",
		Aliases: []string{"vlans"},
	}
	cmd.AddCommand(newVLANListCmd(flags))
	cmd.AddCommand(newVLANGetCmd(flags))
	cmd.AddCommand(newVLANCreateCmd(flags))
	cmd.AddCommand(newVLANUpdateCmd(flags))
	cmd.AddCommand(newVLANDeleteCmd(flags))
	cmd.AddCommand(newVLANPrefixesCmd(flags))
	cmd.AddCommand(newVLANGroupCmd(flags))
	return cmd
}

func vlanTable(vlans []models.VLAN) ([]string, [][]string) {
	headers := []string{"ID", "VID", "NAME", "GROUP", "STATUS", "PREFIXES", "DESCRIPTION"}
	rows := make([][]string, 0, len(vlans))
	for _, v := range vlans {
		rows = append(rows, []string{
			v.ID, strconv.Itoa(v.VID), v.Name, common.Str(v.GroupName),
			string(v.Status), strconv.Itoa(v.PrefixCount), common.Str(v.Description),
		})
	}
	return headers, rows
}

func newVLANListCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List VLANs",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			p := common.ListParams(cmd)
			addFilter(&p, "group_id", "group-id", cmd)
			addFilter(&p, "status", "status", cmd)
			page, err := rc.client.ListVLANs(rc.ctx, p)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return vlanTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	cmd.Flags().String("group-id", "", `Filter by VLAN group ("none" for ungrouped)`)
	cmd.Flags().String("status", "", "Filter by status (active, reserved, deprecated)")
	return cmd
}

// addFilter copies a set string flag into the request's query parameters.
// flagName is the kebab-case CLI form of the snake_case query key.
func addFilter(p *client.ListParams, key, flagName string, cmd *cobra.Command) {
	v, _ := cmd.Flags().GetString(flagName)
	if v == "" {
		return
	}
	if p.Extra == nil {
		p.Extra = url.Values{}
	}
	p.Extra.Set(key, v)
}

func newVLANGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <vlan-id>",
		Short: "Get a single VLAN",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			v, err := rc.client.GetVLAN(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, v, func() ([]string, [][]string) { return vlanTable([]models.VLAN{*v}) })
		},
	}
}

func newVLANPrefixesCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "prefixes <vlan-id>",
		Short: "List the IPAM prefixes assigned to a VLAN",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListVLANPrefixes(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return prefixTable(page.Items) })
		},
	}
}

func newVLANCreateCmd(flags *persistentFlags) *cobra.Command {
	var vid int
	var name, description, groupID, status string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a VLAN",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.VLANCreateRequest{VID: vid, Name: name, Status: status}
			if description != "" {
				req.Description = &description
			}
			if groupID != "" {
				req.GroupID = &groupID
			}
			v, err := rc.client.CreateVLAN(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, v, func() ([]string, [][]string) { return vlanTable([]models.VLAN{*v}) })
		},
	}
	cmd.Flags().IntVar(&vid, "vid", 0, "802.1Q VLAN ID, 1-4094 (required)")
	cmd.Flags().StringVar(&name, "name", "", "VLAN name (required)")
	cmd.Flags().StringVar(&groupID, "group-id", "", "VLAN group to place this VLAN in")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.Flags().StringVar(&status, "status", "", "active (default), reserved, or deprecated")
	cmd.MarkFlagRequired("vid")
	cmd.MarkFlagRequired("name")
	return cmd
}

func newVLANUpdateCmd(flags *persistentFlags) *cobra.Command {
	var vid int
	var name, description, groupID, status string

	cmd := &cobra.Command{
		Use:   "update <vlan-id>",
		Short: "Update a VLAN",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.VLANUpdateRequest{
				Name:        name,
				VID:         common.IntFlagPtr(cmd, "vid"),
				GroupID:     common.StringFlagPtr(cmd, "group-id"),
				Description: common.StringFlagPtr(cmd, "description"),
				Status:      status,
			}
			v, err := rc.client.UpdateVLAN(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, v, func() ([]string, [][]string) { return vlanTable([]models.VLAN{*v}) })
		},
	}
	cmd.Flags().IntVar(&vid, "vid", 0, "New VLAN ID")
	cmd.Flags().StringVar(&name, "name", "", "New name (required)")
	cmd.Flags().StringVar(&groupID, "group-id", "", "New VLAN group")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().StringVar(&status, "status", "", "New status")
	cmd.MarkFlagRequired("name")
	return cmd
}

func newVLANDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <vlan-id>",
		Short: "Delete a VLAN (assigned prefixes are unassigned, not deleted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeleteVLAN(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "vlan %s deleted\n", args[0])
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// vlan group ...
// ---------------------------------------------------------------------------

func newVLANGroupCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "group",
		Short:   "Manage VLAN groups (a layer-2 domain in which each VID is unique)",
		Aliases: []string{"groups"},
	}
	cmd.AddCommand(newVLANGroupListCmd(flags))
	cmd.AddCommand(newVLANGroupCreateCmd(flags))
	cmd.AddCommand(newVLANGroupDeleteCmd(flags))
	return cmd
}

func vlanGroupTable(groups []models.VLANGroup) ([]string, [][]string) {
	headers := []string{"ID", "NAME", "VID_RANGE", "VRF_ID", "DESCRIPTION"}
	rows := make([][]string, 0, len(groups))
	for _, g := range groups {
		rows = append(rows, []string{
			g.ID, g.Name, fmt.Sprintf("%d-%d", g.MinVID, g.MaxVID),
			common.Str(g.VRFID), common.Str(g.Description),
		})
	}
	return headers, rows
}

func newVLANGroupListCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List VLAN groups",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListVLANGroups(rc.ctx, common.ListParams(cmd))
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return vlanGroupTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	return cmd
}

func newVLANGroupCreateCmd(flags *persistentFlags) *cobra.Command {
	var name, description, vrfID string
	var minVID, maxVID int

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a VLAN group",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.VLANGroupRequest{
				Name:   name,
				MinVID: common.IntFlagPtr(cmd, "min-vid"),
				MaxVID: common.IntFlagPtr(cmd, "max-vid"),
			}
			if description != "" {
				req.Description = &description
			}
			if vrfID != "" {
				req.VRFID = &vrfID
			}
			g, err := rc.client.CreateVLANGroup(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, g, func() ([]string, [][]string) { return vlanGroupTable([]models.VLANGroup{*g}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Group name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.Flags().StringVar(&vrfID, "vrf-id", "", "Scope the group to a VRF")
	cmd.Flags().IntVar(&minVID, "min-vid", 0, "Lowest VID this group allows (default 1)")
	cmd.Flags().IntVar(&maxVID, "max-vid", 0, "Highest VID this group allows (default 4094)")
	cmd.MarkFlagRequired("name")
	return cmd
}

func newVLANGroupDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <group-id>",
		Short: "Delete a VLAN group (member VLANs survive as ungrouped)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeleteVLANGroup(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "vlan group %s deleted\n", args[0])
			return nil
		},
	}
}
