package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/config"
)

type configResponse struct {
	APIAddr       string   `json:"api_addr"`
	APITLSEnabled bool     `json:"api_tls_enabled"`
	DatabaseDSN   string   `json:"database_dsn"` // redacted: credentials stripped
	HAEnabled     bool     `json:"ha_enabled"`
	HAClusterID   string   `json:"ha_cluster_id,omitempty"`
	DHCPEnabled   bool     `json:"dhcp_enabled"`
	DHCPIfaces    []string `json:"dhcp_interfaces,omitempty"`
	LoggingLevel  string   `json:"logging_level"`
	LoggingFormat string   `json:"logging_format"`
	MetricsAddr   string   `json:"metrics_addr,omitempty"`
}

// handleGetConfig handles GET /api/v1/config: a read-only snapshot of the
// running configuration with secrets (the DSN's credentials, the JWT
// signing secret) redacted.
func handleGetConfig(cfg *config.Config, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ifaces []string
		for _, i := range cfg.DHCP.Interfaces {
			ifaces = append(ifaces, i.Name)
		}

		resp := configResponse{
			APIAddr:       cfg.Server.APIAddr,
			APITLSEnabled: cfg.Server.APITLSEnabled,
			DatabaseDSN:   redactDSN(cfg.Database.DSN),
			HAEnabled:     cfg.HA.Enabled,
			HAClusterID:   cfg.HA.ClusterID,
			DHCPEnabled:   len(cfg.DHCP.Interfaces) > 0,
			DHCPIfaces:    ifaces,
			LoggingLevel:  cfg.Logging.Level,
			LoggingFormat: cfg.Logging.Format,
			MetricsAddr:   cfg.Metrics.ListenAddr,
		}
		httpx.RespondOK(w, resp)
	}
}

// redactDSN strips everything but the scheme and host:port/dbname shape of a
// postgres DSN so operators can confirm which database gdhcp is pointed at
// without exposing credentials over the API.
func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	scheme, rest, ok := strings.Cut(dsn, "://")
	if !ok {
		return "***redacted***"
	}
	if _, hostpart, ok := strings.Cut(rest, "@"); ok {
		rest = hostpart
	}
	return scheme + "://***:***@" + rest
}

type reloadRequest struct {
	Source string `json:"source"`
}

// handleConfigReload handles POST /api/v1/config/reload. Configuration is
// DB-driven (scopes/pools/options/templates load per-request or via the
// change-notification path in docs/CONTRACTS.md Part 7), so there is no
// static file to re-parse; this endpoint acknowledges the request and points
// the caller at how config actually propagates instead of performing a
// no-op silently.
func handleConfigReload(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req reloadRequest
		_ = httpx.DecodeJSON(r, &req) // source is informational only

		httpx.RespondOK(w, map[string]string{
			"message": "gdhcp configuration is database-driven; DHCP/IPAM changes take effect immediately on write and propagate to other cluster nodes via LISTEN/NOTIFY with polling fallback (docs/CONTRACTS.md Part 7). There is no static file to reload.",
		})
	}
}
