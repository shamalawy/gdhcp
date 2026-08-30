package config

import "fmt"

// Validate checks the full configuration for internal consistency. It does
// not mutate the config (e.g. it does not generate a JWT secret — that is
// the loader's job after validation succeeds).
func (c *Config) Validate() error {
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("server config: %w", err)
	}
	if err := c.Database.Validate(); err != nil {
		return fmt.Errorf("database config: %w", err)
	}
	if err := c.DHCP.Validate(); err != nil {
		return fmt.Errorf("dhcp config: %w", err)
	}
	if err := c.HA.Validate(); err != nil {
		return fmt.Errorf("ha config: %w", err)
	}
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("logging config: %w", err)
	}
	if err := c.Metrics.Validate(); err != nil {
		return fmt.Errorf("metrics config: %w", err)
	}
	return nil
}

// Validate checks ServerConfig.
func (s ServerConfig) Validate() error {
	if s.APIAddr == "" {
		return fmt.Errorf("api_addr must not be empty")
	}
	if s.APITLSEnabled {
		if s.APITLSCert == "" || s.APITLSKey == "" {
			return fmt.Errorf("api_tls_cert and api_tls_key are required when api_tls_enabled is true")
		}
	}
	return nil
}

// Validate checks DatabaseConfig.
func (d DatabaseConfig) Validate() error {
	if d.DSN == "" {
		return fmt.Errorf("dsn must not be empty")
	}
	if d.MaxOpenConns <= 0 {
		return fmt.Errorf("max_open_conns must be > 0, got %d", d.MaxOpenConns)
	}
	if d.MaxIdleConns < 0 {
		return fmt.Errorf("max_idle_conns must be >= 0, got %d", d.MaxIdleConns)
	}
	if d.MaxIdleConns > d.MaxOpenConns {
		return fmt.Errorf("max_idle_conns (%d) must be <= max_open_conns (%d)", d.MaxIdleConns, d.MaxOpenConns)
	}
	if d.LeaseRetentionDays < 0 {
		return fmt.Errorf("lease_retention_days must be >= 0, got %d", d.LeaseRetentionDays)
	}
	return nil
}

// Validate checks DHCPConfig. An empty Interfaces list is valid (amendment
// A11: API/UI-only mode).
func (d DHCPConfig) Validate() error {
	seen := make(map[string]struct{}, len(d.Interfaces))
	for _, iface := range d.Interfaces {
		if iface.Name == "" {
			return fmt.Errorf("dhcp interface entry missing name")
		}
		if iface.VRFID == "" {
			return fmt.Errorf("dhcp interface %q missing vrf_id", iface.Name)
		}
		if _, dup := seen[iface.Name]; dup {
			return fmt.Errorf("dhcp interface %q listed more than once", iface.Name)
		}
		seen[iface.Name] = struct{}{}
	}
	if d.DefaultLeaseTime <= 0 {
		return fmt.Errorf("default_lease_time must be > 0, got %d", d.DefaultLeaseTime)
	}
	if d.DefaultRenewalTime <= 0 || d.DefaultRenewalTime >= d.DefaultLeaseTime {
		return fmt.Errorf("default_renewal_time (%d) must be > 0 and < default_lease_time (%d)", d.DefaultRenewalTime, d.DefaultLeaseTime)
	}
	if d.DefaultRebindTime <= d.DefaultRenewalTime || d.DefaultRebindTime >= d.DefaultLeaseTime {
		return fmt.Errorf("default_rebind_time (%d) must be > default_renewal_time (%d) and < default_lease_time (%d)", d.DefaultRebindTime, d.DefaultRenewalTime, d.DefaultLeaseTime)
	}
	return nil
}

// Validate checks HAConfig. Per docs/ARCHITECTURE.md "Minor: Configuration
// Validation Missing": heartbeat_ttl_ms must be at least 2x
// heartbeat_interval_ms so a single missed heartbeat doesn't trigger a false
// failover, and election_interval_ms should be <= half the TTL for prompt
// failure detection. These checks only apply when HA is enabled — a
// standalone node (amendment A11) doesn't contend elections.
func (h HAConfig) Validate() error {
	if !h.Enabled {
		return nil
	}
	if h.ClusterID == "" {
		return fmt.Errorf("cluster_id is required when ha.enabled is true")
	}
	if h.ServerID == "" {
		return fmt.Errorf("server_id is required when ha.enabled is true")
	}
	if h.NodeID <= 0 {
		return fmt.Errorf("node_id must be a positive integer when ha.enabled is true, got %d", h.NodeID)
	}
	if h.HeartbeatIntervalMs <= 0 {
		return fmt.Errorf("heartbeat_interval_ms must be > 0, got %d", h.HeartbeatIntervalMs)
	}
	if h.HeartbeatTTLMs < 2*h.HeartbeatIntervalMs {
		return fmt.Errorf(
			"heartbeat_ttl_ms (%d) must be >= 2 * heartbeat_interval_ms (%d)",
			h.HeartbeatTTLMs, h.HeartbeatIntervalMs,
		)
	}
	if h.ElectionIntervalMs <= 0 {
		return fmt.Errorf("election_interval_ms must be > 0, got %d", h.ElectionIntervalMs)
	}
	if h.ElectionIntervalMs > h.HeartbeatTTLMs/2 {
		return fmt.Errorf(
			"election_interval_ms (%d) should be <= heartbeat_ttl_ms / 2 (%d) for prompt failure detection",
			h.ElectionIntervalMs, h.HeartbeatTTLMs/2,
		)
	}
	return nil
}

// Validate checks LoggingConfig.
func (l LoggingConfig) Validate() error {
	switch l.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("level must be one of debug|info|warn|error, got %q", l.Level)
	}
	switch l.Format {
	case "json", "text":
	default:
		return fmt.Errorf("format must be one of json|text, got %q", l.Format)
	}
	switch l.Output {
	case "stdout", "file", "syslog":
	default:
		return fmt.Errorf("output must be one of stdout|file|syslog, got %q", l.Output)
	}
	return nil
}

// Validate checks MetricsConfig.
func (m MetricsConfig) Validate() error {
	if !m.Enabled {
		return nil
	}
	if m.ListenAddr == "" {
		return fmt.Errorf("listen_addr must not be empty when metrics.enabled is true")
	}
	if m.Path == "" {
		return fmt.Errorf("path must not be empty when metrics.enabled is true")
	}
	return nil
}
