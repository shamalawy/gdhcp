package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/config"
	"github.com/shamalawy/gdhcp/server/internal/db"
	"github.com/shamalawy/gdhcp/server/internal/server"
)

// defaultServerConfigPaths are tried, in order, when --config-file is not
// given: the packaged system location first, then a config.yaml next to
// the working directory for local/dev runs.
var defaultServerConfigPaths = []string{"/etc/gdhcp/config.yaml", "config.yaml"}

func newServeCmd() *cobra.Command {
	var configFile string
	var autoMigrate bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the gdhcp server (DHCP engine, REST API, and embedded UI)",
		Long: "serve loads the server configuration, sets up structured logging, optionally runs " +
			"pending database migrations, and then runs gdhcp until it receives SIGINT/SIGTERM, " +
			"shutting down gracefully.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := configFile
			if path == "" {
				path = firstExistingPath(defaultServerConfigPaths)
			}

			cfg, err := config.Load(path)
			if err != nil {
				return fmt.Errorf("serve: load config: %w", err)
			}

			logger := newLogger(cfg.Logging)
			slog.SetDefault(logger)
			logger.Info("gdhcp starting", "config_file", path, "auto_migrate", autoMigrate)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if autoMigrate {
				if err := runMigrations(ctx, cfg); err != nil {
					return fmt.Errorf("serve: auto-migrate: %w", err)
				}
			}

			if err := server.Run(ctx, cfg); err != nil {
				return fmt.Errorf("serve: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&configFile, "config-file", "", "Server config.yaml path (default: /etc/gdhcp/config.yaml, then ./config.yaml)")
	cmd.Flags().BoolVar(&autoMigrate, "auto-migrate", true, "Run pending database migrations on startup before serving")

	return cmd
}

// firstExistingPath returns the first path in candidates that exists, or
// the last candidate if none do (so the resulting error names a real
// attempted path rather than an empty string).
func firstExistingPath(candidates []string) string {
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if len(candidates) > 0 {
		return candidates[len(candidates)-1]
	}
	return ""
}

// newLogger builds the stdlib slog logger configured by cfg (amendment
// A7: log/slog, not logrus), writing to stdout in either JSON or text
// handler form. "file" and "syslog" outputs are accepted by config
// validation for forward compatibility but not yet wired to a real
// destination here; both currently log to stdout with a warning so
// startup never silently drops logs.
func newLogger(cfg config.LoggingConfig) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)
	if cfg.Output != "stdout" {
		logger.Warn("logging.output is not stdout; file/syslog destinations are not yet implemented, logging to stdout instead", "configured_output", cfg.Output)
	}
	return logger
}

// runMigrations opens a short-lived pool against cfg.Database and runs
// pending migrations through it, closing the pool afterward — used by both
// `serve --auto-migrate` and `gdhcp migrate`.
func runMigrations(ctx context.Context, cfg *config.Config) error {
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	if err := db.NewMigrator(pool).Run(ctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
