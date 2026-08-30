// Package config defines the gdhcp configuration schema (docs/CONTRACTS.md
// Part 4, amended by docs/AMENDMENTS.md A11), and loads it from a YAML file
// with GDHCP_* environment variable overrides.
package config

import "time"

// Config is the root configuration structure, mirroring config.yaml.example.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	DHCP     DHCPConfig     `yaml:"dhcp"`
	HA       HAConfig       `yaml:"ha"`
	Logging  LoggingConfig  `yaml:"logging"`
	Metrics  MetricsConfig  `yaml:"metrics"`
}

// ServerConfig configures the HTTP API listener.
type ServerConfig struct {
	APIAddr       string `yaml:"api_addr"`
	APITLSEnabled bool   `yaml:"api_tls_enabled"`
	APITLSCert    string `yaml:"api_tls_cert"`
	APITLSKey     string `yaml:"api_tls_key"`

	// JWTSecret signs session JWTs (amendment A11). If empty, a random
	// secret is generated at startup and all existing sessions are
	// invalidated on restart — acceptable for dev/single-node, not for a
	// multi-node deployment sharing session state.
	JWTSecret string `yaml:"jwt_secret"`

	// CORSAllowedOrigins is the explicit allowlist of Origins the API will
	// reflect back with Access-Control-Allow-Origin/-Credentials (e.g. a
	// local frontend dev server such as "http://localhost:4200"). Empty by
	// default: amendment A4 serves the SPA same-origin in production, where
	// no cross-origin request needs to be allowed at all. An Origin not on
	// this list gets no CORS headers, not a blanket reflection.
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
}

// DatabaseConfig configures the PostgreSQL connection pool.
type DatabaseConfig struct {
	DSN                string        `yaml:"dsn"`
	MaxOpenConns       int           `yaml:"max_open_conns"`
	MaxIdleConns       int           `yaml:"max_idle_conns"`
	ConnMaxLifetime    time.Duration `yaml:"conn_max_lifetime"`
	LeaseRetentionDays int           `yaml:"lease_retention_days"`
	OptionCacheTTL     time.Duration `yaml:"option_cache_ttl"`
}

// DHCPInterface binds a network interface to a VRF for DHCP listening.
type DHCPInterface struct {
	Name  string `yaml:"name"`
	VRFID string `yaml:"vrf_id"`
}

// DHCPConfig configures the DHCP engine. Per amendment A11, an empty
// Interfaces list is valid and means API/UI-only mode: no DHCP listeners are
// started, which is used for IPAM-only nodes and local development/smoke
// tests.
type DHCPConfig struct {
	Interfaces         []DHCPInterface `yaml:"interfaces"`
	DefaultLeaseTime   int             `yaml:"default_lease_time"`   // seconds
	DefaultRenewalTime int             `yaml:"default_renewal_time"` // seconds
	DefaultRebindTime  int             `yaml:"default_rebind_time"`  // seconds
}

// HAConfig configures cluster membership and failover timing. Per amendment
// A11, Enabled=false means standalone single-server mode; the server still
// registers itself in the database but never contends an election.
type HAConfig struct {
	Enabled               bool   `yaml:"enabled"`
	ClusterID             string `yaml:"cluster_id"`
	ServerID              string `yaml:"server_id"`
	ServerName            string `yaml:"server_name"`
	NodeID                int    `yaml:"node_id"`
	VIPAddress            string `yaml:"vip_address"`
	VIPInterface          string `yaml:"vip_interface"`
	HeartbeatIntervalMs   int    `yaml:"heartbeat_interval_ms"`
	HeartbeatTTLMs        int    `yaml:"heartbeat_ttl_ms"`
	ElectionIntervalMs    int    `yaml:"election_interval_ms"`
	ConfigCheckIntervalMs int    `yaml:"config_check_interval_ms"`
	ConfigListenEnabled   bool   `yaml:"config_listen_enabled"`
}

// LoggingConfig configures the slog-based structured logger (amendment A7).
type LoggingConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json or text
	Output string `yaml:"output"` // stdout, file, syslog
}

// MetricsConfig configures the Prometheus metrics endpoint.
type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ListenAddr string `yaml:"listen_addr"`
	Path       string `yaml:"path"`
}
