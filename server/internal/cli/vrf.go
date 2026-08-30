package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newVRFCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "vrf",
		Short:   "Manage VRFs (routing domains that scope IPAM prefixes and DHCP scopes)",
		Aliases: []string{"vrfs"},
	}
	cmd.AddCommand(newVRFListCmd(flags))
	cmd.AddCommand(newVRFGetCmd(flags))
	cmd.AddCommand(newVRFCreateCmd(flags))
	cmd.AddCommand(newVRFUpdateCmd(flags))
	cmd.AddCommand(newVRFDeleteCmd(flags))
	return cmd
}

func vrfTable(vrfs []models.VRF) ([]string, [][]string) {
	headers := []string{"ID", "NAME", "DEFAULT", "ROUTE_DISTINGUISHER", "DESCRIPTION"}
	rows := make([][]string, 0, len(vrfs))
	for _, v := range vrfs {
		rows = append(rows, []string{v.ID, v.Name, common.Bool(v.IsDefault), common.Str(v.RouteDistinguisher), common.Str(v.Description)})
	}
	return headers, rows
}

func newVRFListCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List VRFs",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListVRFs(rc.ctx, common.ListParams(cmd))
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return vrfTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	return cmd
}

func newVRFGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <vrf-id>",
		Short: "Get a single VRF",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			v, err := rc.client.GetVRF(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, v, func() ([]string, [][]string) { return vrfTable([]models.VRF{*v}) })
		},
	}
}

func newVRFCreateCmd(flags *persistentFlags) *cobra.Command {
	var name, description, rd string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a VRF",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.VRFCreateRequest{Name: name}
			if description != "" {
				req.Description = &description
			}
			if rd != "" {
				req.RouteDistinguisher = &rd
			}
			v, err := rc.client.CreateVRF(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, v, func() ([]string, [][]string) { return vrfTable([]models.VRF{*v}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "VRF name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.Flags().StringVar(&rd, "route-distinguisher", "", "Route distinguisher")
	cmd.MarkFlagRequired("name")
	return cmd
}

func newVRFUpdateCmd(flags *persistentFlags) *cobra.Command {
	var name, description, rd string

	cmd := &cobra.Command{
		Use:   "update <vrf-id>",
		Short: "Update a VRF",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.VRFUpdateRequest{
				Name:               common.StringFlagPtr(cmd, "name"),
				Description:        common.StringFlagPtr(cmd, "description"),
				RouteDistinguisher: common.StringFlagPtr(cmd, "route-distinguisher"),
			}
			v, err := rc.client.UpdateVRF(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, v, func() ([]string, [][]string) { return vrfTable([]models.VRF{*v}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().StringVar(&rd, "route-distinguisher", "", "New route distinguisher")
	return cmd
}

func newVRFDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <vrf-id>",
		Short: "Delete a VRF (must have no scopes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeleteVRF(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "vrf %s deleted\n", args[0])
			return nil
		},
	}
}
