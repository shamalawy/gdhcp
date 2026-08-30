package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newUserCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "user",
		Short:   "Manage users and their API tokens (admin only)",
		Aliases: []string{"users"},
	}
	cmd.AddCommand(newUserListCmd(flags))
	cmd.AddCommand(newUserGetCmd(flags))
	cmd.AddCommand(newUserCreateCmd(flags))
	cmd.AddCommand(newUserUpdateCmd(flags))
	cmd.AddCommand(newUserDeleteCmd(flags))
	cmd.AddCommand(newUserTokenCmd(flags))
	return cmd
}

func userTable(users []models.User) ([]string, [][]string) {
	headers := []string{"ID", "USERNAME", "EMAIL", "ROLE", "ACTIVE", "DESCRIPTION"}
	rows := make([][]string, 0, len(users))
	for _, u := range users {
		rows = append(rows, []string{fmt.Sprintf("%d", u.ID), u.Username, u.Email, string(u.Role), common.Bool(u.IsActive), common.Str(u.Description)})
	}
	return headers, rows
}

func newUserListCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List users (admin only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			page, err := rc.client.ListUsers(rc.ctx, common.ListParams(cmd))
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, page, func() ([]string, [][]string) { return userTable(page.Items) })
		},
	}
	common.AddListFlags(cmd)
	return cmd
}

func newUserGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <user-id>",
		Short: "Get a single user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			u, err := rc.client.GetUser(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, u, func() ([]string, [][]string) { return userTable([]models.User{*u}) })
		},
	}
}

func newUserCreateCmd(flags *persistentFlags) *cobra.Command {
	var username, email, password, description string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a user (admin only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.UserCreateRequest{Username: username, Email: email, Password: password}
			if description != "" {
				req.Description = &description
			}
			u, err := rc.client.CreateUser(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, u, func() ([]string, [][]string) { return userTable([]models.User{*u}) })
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "Username (required)")
	cmd.Flags().StringVar(&email, "email", "", "Email (required)")
	cmd.Flags().StringVar(&password, "password", "", "Initial password (required; prefer piping this in over a literal flag on shared machines)")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.MarkFlagRequired("username")
	cmd.MarkFlagRequired("email")
	cmd.MarkFlagRequired("password")
	return cmd
}

func newUserUpdateCmd(flags *persistentFlags) *cobra.Command {
	var username, email, description string
	var active bool
	cmd := &cobra.Command{
		Use:   "update <user-id>",
		Short: "Update a user (admin only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.UserUpdateRequest{
				Username:    common.StringFlagPtr(cmd, "username"),
				Email:       common.StringFlagPtr(cmd, "email"),
				Description: common.StringFlagPtr(cmd, "description"),
				IsActive:    common.BoolFlagPtr(cmd, "active"),
			}
			u, err := rc.client.UpdateUser(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, u, func() ([]string, [][]string) { return userTable([]models.User{*u}) })
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "New username")
	cmd.Flags().StringVar(&email, "email", "", "New email")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().BoolVar(&active, "active", true, "Activate or deactivate the user")
	return cmd
}

func newUserDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <user-id>",
		Short: "Delete a user (admin only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeleteUser(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "user %s deleted\n", args[0])
			return nil
		},
	}
}

func newUserTokenCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage a user's API tokens",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list <user-id>",
		Short: "List a user's API tokens",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			tokens, err := rc.client.ListUserTokens(rc.ctx, args[0], common.ListParams(cmd))
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, tokens, func() ([]string, [][]string) {
				headers := []string{"ID", "DESCRIPTION", "LAST_USED_AT", "EXPIRES_AT", "CREATED_AT"}
				rows := make([][]string, 0, len(tokens))
				for _, t := range tokens {
					rows = append(rows, []string{fmt.Sprintf("%d", t.ID), common.Str(t.Description), common.TimePtr(t.LastUsedAt), common.TimePtr(t.ExpiresAt), common.Time(t.CreatedAt)})
				}
				return headers, rows
			})
		},
	})

	var description string
	var expiresInDays int
	createCmd := &cobra.Command{
		Use:   "create <user-id>",
		Short: "Create an API token for a user (the raw token is shown exactly once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.TokenCreateRequest{Description: description}
			if expiresInDays > 0 {
				t := time.Now().AddDate(0, 0, expiresInDays)
				req.ExpiresAt = &t
			}
			tok, err := rc.client.CreateUserToken(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, tok, func() ([]string, [][]string) {
				return []string{"ID", "TOKEN", "DESCRIPTION", "EXPIRES_AT"}, [][]string{{fmt.Sprintf("%d", tok.ID), tok.Token, common.Str(tok.Description), common.TimePtr(tok.ExpiresAt)}}
			})
		},
	}
	createCmd.Flags().StringVar(&description, "description", "", "Token description (required)")
	createCmd.Flags().IntVar(&expiresInDays, "expires-in-days", 0, "Expire the token after N days (0 = never)")
	createCmd.MarkFlagRequired("description")
	cmd.AddCommand(createCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <user-id> <token-id>",
		Short: "Revoke an API token",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.RevokeUserToken(rc.ctx, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "token %s revoked\n", args[1])
			return nil
		},
	})

	return cmd
}
