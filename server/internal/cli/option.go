package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newOptionCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "option",
		Short:   "Manage DHCP option definitions and browse the IANA option catalog",
		Aliases: []string{"options"},
	}
	cmd.AddCommand(newOptionListCmd(flags))
	cmd.AddCommand(newOptionCatalogCmd(flags))
	cmd.AddCommand(newOptionGetCmd(flags))
	cmd.AddCommand(newOptionCreateCmd(flags))
	cmd.AddCommand(newOptionDeleteCmd(flags))
	return cmd
}

func optionTable(opts []*models.DHCPOption) ([]string, [][]string) {
	headers := []string{"ID", "CODE", "NAME", "SCOPE_LEVEL", "ENTITY_ID", "VALUE", "ENABLED"}
	rows := make([][]string, 0, len(opts))
	for _, o := range opts {
		name := "-"
		if o.Catalog != nil {
			name = o.Catalog.Name
		}
		rows = append(rows, []string{o.ID, fmt.Sprintf("%d", o.OptionCode), name, string(o.ScopeLevel), o.ScopeEntityID, o.Value, common.Bool(o.IsEnabled)})
	}
	return headers, rows
}

func newOptionListCmd(flags *persistentFlags) *cobra.Command {
	var standard, custom bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured option values (standard and/or custom)",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			opts, err := rc.client.ListOptions(rc.ctx, common.ListParams(cmd), standard, custom)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, opts, func() ([]string, [][]string) { return optionTable(opts) })
		},
	}
	common.AddListFlags(cmd)
	cmd.Flags().BoolVar(&standard, "standard", false, "Include only standard (IANA catalog) options")
	cmd.Flags().BoolVar(&custom, "custom", false, "Include only custom options")
	return cmd
}

func newOptionCatalogCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "catalog",
		Short: "List the full IANA DHCPv4 option catalog with descriptions",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			entries, err := rc.client.OptionCatalog(rc.ctx)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, entries, func() ([]string, [][]string) {
				headers := []string{"CODE", "NAME", "VALUE_TYPE", "DESCRIPTION", "RFC"}
				rows := make([][]string, 0, len(entries))
				for _, e := range entries {
					rfc := "-"
					if e.IANAReference != nil {
						rfc = *e.IANAReference
					}
					rows = append(rows, []string{fmt.Sprintf("%d", e.OptionCode), e.Name, string(e.ValueType), e.Description, rfc})
				}
				return headers, rows
			})
		},
	}
}

func newOptionGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <option-id>",
		Short: "Get a single configured option value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			o, err := rc.client.GetOption(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, o, func() ([]string, [][]string) { return optionTable([]*models.DHCPOption{o}) })
		},
	}
}

func newOptionCreateCmd(flags *persistentFlags) *cobra.Command {
	var name, description, valueType string
	var code int
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a custom DHCP option definition",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.OptionCreateRequest{Name: name, Description: description, Code: int16(code), ValueType: valueType}
			o, err := rc.client.CreateOption(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, o, func() ([]string, [][]string) { return optionTable([]*models.DHCPOption{o}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Option name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Description (required — every option carries one, per quality bar)")
	cmd.Flags().IntVar(&code, "code", 0, "DHCP option code, 1-254 (required)")
	cmd.Flags().StringVar(&valueType, "value-type", "", "string|uint8|uint16|uint32|ip|ip_list|bytes|boolean|fqdn|route_list (required)")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("description")
	cmd.MarkFlagRequired("code")
	cmd.MarkFlagRequired("value-type")
	return cmd
}

func newOptionDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <option-id>",
		Short: "Delete a custom option definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeleteOption(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "option %s deleted\n", args[0])
			return nil
		},
	}
}
