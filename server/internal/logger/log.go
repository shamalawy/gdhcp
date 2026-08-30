// Package logger configures gdhcp's structured logging. Per amendment A7,
// this uses the standard library's log/slog — not logrus, despite
// docs/FILETREE.md and docs/ARCHITECTURE.md's dependency list saying
// otherwise — and does not implement OpenTelemetry tracing.
package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/shamalawy/gdhcp/server/internal/config"
)

// New builds a slog.Logger per cfg.Logging: level and format (json/text)
// come from config, output currently supports stdout/stderr-style streams
// via a caller-supplied writer selection (file/syslog outputs are left to
// the caller to open and pass through NewWithWriter — this constructor
// implements the common "stdout" case directly).
func New(cfg config.LoggingConfig) (*slog.Logger, error) {
	level, err := ParseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	var w *os.File
	switch cfg.Output {
	case "", "stdout":
		w = os.Stdout
	case "file", "syslog":
		// The Foundation unit does not own file rotation or syslog
		// transport; callers needing those should open the appropriate
		// writer and call NewWithWriter directly. Falling back to stdout
		// keeps `gdhcp serve` usable out of the box rather than erroring.
		w = os.Stdout
	default:
		return nil, fmt.Errorf("logger: unknown output %q", cfg.Output)
	}

	return NewWithWriter(w, cfg.Format, level)
}

// NewWithWriter builds a slog.Logger writing to w in the given format
// ("json" or "text") at the given level.
func NewWithWriter(w *os.File, format string, level slog.Level) (*slog.Logger, error) {
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch format {
	case "", "json":
		handler = slog.NewJSONHandler(w, opts)
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("logger: unknown format %q (want json|text)", format)
	}

	return slog.New(handler), nil
}

// ParseLevel converts a config-file level string to a slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logger: unknown level %q (want debug|info|warn|error)", s)
	}
}
