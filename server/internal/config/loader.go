package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load reads configuration from the YAML file at path (if non-empty and it
// exists), applies GDHCP_* environment variable overrides on top, validates
// the result, and — per amendment A11 — generates a random JWT secret if
// none was configured. A missing file is not an error: defaults plus env
// overrides alone are enough to run (e.g. for local dev/smoke tests).
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("config: parse %s: %w", path, err)
			}
		case os.IsNotExist(err):
			// Fall through: defaults + env overrides only.
		default:
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
	}

	if err := applyEnvOverrides(cfg); err != nil {
		return nil, fmt.Errorf("config: env override: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	if cfg.Server.JWTSecret == "" {
		secret, err := randomSecret(32)
		if err != nil {
			return nil, fmt.Errorf("config: generate jwt secret: %w", err)
		}
		cfg.Server.JWTSecret = secret
	}

	return cfg, nil
}

// randomSecret returns a URL-safe base64 string encoding n random bytes.
func randomSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func envString(key string, dst *string) {
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}

// envStringList overrides dst with a comma-separated env var, trimming
// whitespace around each entry and dropping empty ones (so
// "a, b,,c" -> []string{"a", "b", "c"}, and an explicitly-set empty value
// clears dst to an empty, non-nil slice rather than leaving it unchanged).
func envStringList(key string, dst *[]string) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return
	}
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	*dst = out
}

func envBool(key string, dst *bool) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fmt.Errorf("%s: invalid bool %q: %w", key, v, err)
	}
	*dst = b
	return nil
}

func envInt(key string, dst *int) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%s: invalid int %q: %w", key, v, err)
	}
	*dst = i
	return nil
}

func envDuration(key string, dst *time.Duration) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("%s: invalid duration %q: %w", key, v, err)
	}
	*dst = d
	return nil
}

// applyEnvOverrides layers GDHCP_* environment variables over cfg. Env
// overrides win over the YAML file — the precedence deployments rely on
// (docs/design/deploy-build.md) to inject secrets and per-node identity
// (GDHCP_SERVER_ID, GDHCP_CLUSTER_ID) without templating the config file.
func applyEnvOverrides(cfg *Config) error {
	envString("GDHCP_SERVER_API_ADDR", &cfg.Server.APIAddr)
	if err := envBool("GDHCP_SERVER_API_TLS_ENABLED", &cfg.Server.APITLSEnabled); err != nil {
		return err
	}
	envString("GDHCP_SERVER_API_TLS_CERT", &cfg.Server.APITLSCert)
	envString("GDHCP_SERVER_API_TLS_KEY", &cfg.Server.APITLSKey)
	envString("GDHCP_SERVER_JWT_SECRET", &cfg.Server.JWTSecret)
	envStringList("GDHCP_SERVER_CORS_ALLOWED_ORIGINS", &cfg.Server.CORSAllowedOrigins)

	envString("GDHCP_DATABASE_DSN", &cfg.Database.DSN)
	if err := envInt("GDHCP_DATABASE_MAX_OPEN_CONNS", &cfg.Database.MaxOpenConns); err != nil {
		return err
	}
	if err := envInt("GDHCP_DATABASE_MAX_IDLE_CONNS", &cfg.Database.MaxIdleConns); err != nil {
		return err
	}
	if err := envDuration("GDHCP_DATABASE_CONN_MAX_LIFETIME", &cfg.Database.ConnMaxLifetime); err != nil {
		return err
	}
	if err := envInt("GDHCP_DATABASE_LEASE_RETENTION_DAYS", &cfg.Database.LeaseRetentionDays); err != nil {
		return err
	}
	if err := envDuration("GDHCP_DATABASE_OPTION_CACHE_TTL", &cfg.Database.OptionCacheTTL); err != nil {
		return err
	}

	if err := envInt("GDHCP_DHCP_DEFAULT_LEASE_TIME", &cfg.DHCP.DefaultLeaseTime); err != nil {
		return err
	}
	if err := envInt("GDHCP_DHCP_DEFAULT_RENEWAL_TIME", &cfg.DHCP.DefaultRenewalTime); err != nil {
		return err
	}
	if err := envInt("GDHCP_DHCP_DEFAULT_REBIND_TIME", &cfg.DHCP.DefaultRebindTime); err != nil {
		return err
	}
	if v, ok := os.LookupEnv("GDHCP_DHCP_INTERFACES"); ok {
		cfg.DHCP.Interfaces = parseInterfacesEnv(v)
	}

	if err := envBool("GDHCP_HA_ENABLED", &cfg.HA.Enabled); err != nil {
		return err
	}
	envString("GDHCP_HA_CLUSTER_ID", &cfg.HA.ClusterID)
	envString("GDHCP_HA_SERVER_ID", &cfg.HA.ServerID)
	envString("GDHCP_HA_SERVER_NAME", &cfg.HA.ServerName)
	if err := envInt("GDHCP_HA_NODE_ID", &cfg.HA.NodeID); err != nil {
		return err
	}
	envString("GDHCP_HA_VIP_ADDRESS", &cfg.HA.VIPAddress)
	envString("GDHCP_HA_VIP_INTERFACE", &cfg.HA.VIPInterface)
	if err := envInt("GDHCP_HA_HEARTBEAT_INTERVAL_MS", &cfg.HA.HeartbeatIntervalMs); err != nil {
		return err
	}
	if err := envInt("GDHCP_HA_HEARTBEAT_TTL_MS", &cfg.HA.HeartbeatTTLMs); err != nil {
		return err
	}
	if err := envInt("GDHCP_HA_ELECTION_INTERVAL_MS", &cfg.HA.ElectionIntervalMs); err != nil {
		return err
	}
	if err := envInt("GDHCP_HA_CONFIG_CHECK_INTERVAL_MS", &cfg.HA.ConfigCheckIntervalMs); err != nil {
		return err
	}
	if err := envBool("GDHCP_HA_CONFIG_LISTEN_ENABLED", &cfg.HA.ConfigListenEnabled); err != nil {
		return err
	}

	envString("GDHCP_LOGGING_LEVEL", &cfg.Logging.Level)
	envString("GDHCP_LOGGING_FORMAT", &cfg.Logging.Format)
	envString("GDHCP_LOGGING_OUTPUT", &cfg.Logging.Output)

	if err := envBool("GDHCP_METRICS_ENABLED", &cfg.Metrics.Enabled); err != nil {
		return err
	}
	envString("GDHCP_METRICS_LISTEN_ADDR", &cfg.Metrics.ListenAddr)
	envString("GDHCP_METRICS_PATH", &cfg.Metrics.Path)

	return nil
}

// parseInterfacesEnv parses GDHCP_DHCP_INTERFACES as a comma-separated list
// of "name:vrf_id" pairs (e.g. "eth0:default,eth1:mgmt"), for deployments
// that inject interface bindings without a config file (container/K8s).
// Malformed entries are skipped rather than erroring, since this is a
// convenience override, not the primary configuration path.
func parseInterfacesEnv(v string) []DHCPInterface {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]DHCPInterface, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, vrf, found := strings.Cut(p, ":")
		if !found || name == "" || vrf == "" {
			continue
		}
		out = append(out, DHCPInterface{Name: name, VRFID: vrf})
	}
	return out
}
