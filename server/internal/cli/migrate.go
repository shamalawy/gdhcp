package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/config"
)

func newMigrateCmd() *cobra.Command {
	var configFile string

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run pending database migrations and exit",
		Long:  "migrate loads the server configuration, applies every pending embedded migration to the configured database, and exits — it does not start the server.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := configFile
			if path == "" {
				path = firstExistingPath(defaultServerConfigPaths)
			}

			cfg, err := config.Load(path)
			if err != nil {
				return fmt.Errorf("migrate: load config: %w", err)
			}

			if err := runMigrations(context.Background(), cfg); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "migrations applied successfully")
			return nil
		},
	}

	cmd.Flags().StringVar(&configFile, "config-file", "", "Server config.yaml path (default: /etc/gdhcp/config.yaml, then ./config.yaml)")
	return cmd
}
