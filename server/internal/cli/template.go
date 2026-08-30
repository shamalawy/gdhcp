package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newTemplateCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "template",
		Short:   "Manage DHCP option templates (reusable option bundles attachable to scopes)",
		Aliases: []string{"templates"},
	}
	cmd.AddCommand(newTemplateListCmd(flags))
	cmd.AddCommand(newTemplateGetCmd(flags))
	cmd.AddCommand(newTemplateCreateCmd(flags))
	cmd.AddCommand(newTemplateUpdateCmd(flags))
	cmd.AddCommand(newTemplateDeleteCmd(flags))
	cmd.AddCommand(newTemplateOptionsCmd(flags))
	cmd.AddCommand(newTemplateScopesCmd(flags))
	return cmd
}

func templateTable(templates []*models.Template) ([]string, [][]string) {
	headers := []string{"ID", "NAME", "ENABLED", "DESCRIPTION"}
	rows := make([][]string, 0, len(templates))
	for _, t := range templates {
		rows = append(rows, []string{t.ID, t.Name, common.Bool(t.IsEnabled), common.Str(t.Description)})
	}
	return headers, rows
}

func newTemplateListCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List templates",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			templates, err := rc.client.ListTemplates(rc.ctx, common.ListParams(cmd))
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, templates, func() ([]string, [][]string) { return templateTable(templates) })
		},
	}
	common.AddListFlags(cmd)
	return cmd
}

func newTemplateGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <template-id>",
		Short: "Get a single template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			t, err := rc.client.GetTemplate(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, t, func() ([]string, [][]string) { return templateTable([]*models.Template{t}) })
		},
	}
}

func newTemplateCreateCmd(flags *persistentFlags) *cobra.Command {
	var name, description string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a template",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.TemplateCreateRequest{Name: name}
			if description != "" {
				req.Description = &description
			}
			t, err := rc.client.CreateTemplate(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, t, func() ([]string, [][]string) { return templateTable([]*models.Template{t}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Template name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.MarkFlagRequired("name")
	return cmd
}

func newTemplateUpdateCmd(flags *persistentFlags) *cobra.Command {
	var name, description string
	var enabled bool
	cmd := &cobra.Command{
		Use:   "update <template-id>",
		Short: "Update a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.TemplateUpdateRequest{
				Name:        common.StringFlagPtr(cmd, "name"),
				Description: common.StringFlagPtr(cmd, "description"),
				IsEnabled:   common.BoolFlagPtr(cmd, "enabled"),
			}
			t, err := rc.client.UpdateTemplate(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, t, func() ([]string, [][]string) { return templateTable([]*models.Template{t}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "Enable or disable the template")
	return cmd
}

func newTemplateDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <template-id>",
		Short: "Delete a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeleteTemplate(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "template %s deleted\n", args[0])
			return nil
		},
	}
}

func newTemplateOptionsCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Manage DHCP option values set on a template",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "get <template-id>",
		Short: "List option values set on a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			opts, err := rc.client.GetTemplateOptions(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, opts, func() ([]string, [][]string) { return optionValueTable(opts) })
		},
	})

	var value string
	var code int
	setCmd := &cobra.Command{
		Use:   "set <template-id>",
		Short: "Set (create) an option value on a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			ov, err := rc.client.SetTemplateOption(rc.ctx, args[0], int16(code), value)
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

	return cmd
}

func newTemplateScopesCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "scopes <template-id>",
		Short: "List scopes using a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			assocs, err := rc.client.TemplateScopes(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, assocs, func() ([]string, [][]string) {
				headers := []string{"ID", "TEMPLATE_ID", "SCOPE_ID", "PRIORITY"}
				rows := make([][]string, 0, len(assocs))
				for _, a := range assocs {
					rows = append(rows, []string{a.ID, a.TemplateID, a.ScopeID, fmt.Sprintf("%d", a.Priority)})
				}
				return headers, rows
			})
		},
	}
}
