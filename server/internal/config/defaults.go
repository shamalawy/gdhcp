package config

import "time"

// Default returns a Config populated with the same defaults documented in
// config.yaml.example / docs/CONTRACTS.md Part 4. Load starts from this and
// layers the YAML file, then environment overrides, on top.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			APIAddr:       "0.0.0.0:8080",
			APITLSEnabled: false,
		},
		Database: DatabaseConfig{
			DSN:                "postgres://gdhcp:gdhcp@localhost:5432/gdhcp?sslmode=disable",
			MaxOpenConns:       25,
			MaxIdleConns:       5,
			ConnMaxLifetime:    5 * time.Minute,
			LeaseRetentionDays: 90,
			OptionCacheTTL:     60 * time.Second,
		},
		DHCP: DHCPConfig{
			Interfaces:         nil, // Empty = API/UI-only mode (amendment A11)
			DefaultLeaseTime:   86400,
			DefaultRenewalTime: 43200,
			DefaultRebindTime:  75600,
		},
		HA: HAConfig{
			Enabled:               false, // Standalone single-server mode (amendment A11)
			HeartbeatIntervalMs:   1000,
			HeartbeatTTLMs:        3000,
			ElectionIntervalMs:    2000,
			ConfigCheckIntervalMs: 5000,
			ConfigListenEnabled:   true,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
		Metrics: MetricsConfig{
			Enabled:    true,
			ListenAddr: "0.0.0.0:9090",
			Path:       "/metrics",
		},
	}
}
