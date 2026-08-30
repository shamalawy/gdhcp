package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newTagCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tag",
		Short:   "Manage tags and their attachment to entities",
		Aliases: []string{"tags"},
	}
	cmd.AddCommand(newTagListCmd(flags))
	cmd.AddCommand(newTagCreateCmd(flags))
	cmd.AddCommand(newTagDeleteCmd(flags))
	cmd.AddCommand(newTagAttachCmd(flags))
	cmd.AddCommand(newTagDetachCmd(flags))
	return cmd
}

func tagTable(tags []*models.Tag) ([]string, [][]string) {
	headers := []string{"ID", "NAME", "COLOR", "DESCRIPTION"}
	rows := make([][]string, 0, len(tags))
	for _, t := range tags {
		rows = append(rows, []string{t.ID, t.Name, common.Str(t.Color), common.Str(t.Description)})
	}
	return headers, rows
}

func newTagListCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tags",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			tags, err := rc.client.ListTags(rc.ctx, common.ListParams(cmd))
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, tags, func() ([]string, [][]string) { return tagTable(tags) })
		},
	}
	common.AddListFlags(cmd)
	return cmd
}

func newTagCreateCmd(flags *persistentFlags) *cobra.Command {
	var name, description, color string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a tag",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.TagCreateRequest{Name: name}
			if description != "" {
				req.Description = &description
			}
			if color != "" {
				req.Color = &color
			}
			t, err := rc.client.CreateTag(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, t, func() ([]string, [][]string) { return tagTable([]*models.Tag{t}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Tag name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.Flags().StringVar(&color, "color", "", "Hex color, e.g. #3366FF")
	cmd.MarkFlagRequired("name")
	return cmd
}

func newTagDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <tag-id>",
		Short: "Delete a tag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeleteTag(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "tag %s deleted\n", args[0])
			return nil
		},
	}
}

func newTagAttachCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "attach <tag-id> <entity-type> <entity-id>",
		Short: "Attach a tag to an entity (vrf|scope|pool|reservation|server|cluster)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			et, err := rc.client.TagEntity(rc.ctx, args[0], args[1], args[2])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, et, func() ([]string, [][]string) {
				return []string{"ID", "TAG_ID", "ENTITY_TYPE", "ENTITY_ID"}, [][]string{{et.ID, et.TagID, string(et.EntityType), et.EntityID}}
			})
		},
	}
}

func newTagDetachCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "detach <tag-id> <entity-type> <entity-id>",
		Short: "Remove a tag from an entity",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.UntagEntity(rc.ctx, args[0], args[1], args[2]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "tag %s detached from %s %s\n", args[0], args[1], args[2])
			return nil
		},
	}
}
