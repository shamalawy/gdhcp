-- Migration 0001: initial schema
--
-- Source: docs/db/schema.sql, with the following amendments from
-- docs/AMENDMENTS.md applied:
--   A5.1  dhcp_leases.binding_server_id: NOT NULL dropped (contradicted its
--         own ON DELETE SET NULL).
--   A5.2  GiST indexes/exclusion constraints on cidr/inet columns explicitly
--         name the inet_ops opclass.
--   A5.3  The default admin user seed moves to migration 0003
--         (0003_seed_admin.sql), not here.
--   A5.4  dhcp_option_catalog.value_type CHECK constraint gains 'fqdn' and
--         'route_list'.
--   A15   users gains a `role` column (RBAC).
-- The section-11 sample option-catalog INSERT from the schema.sql draft is
-- dropped entirely: migration 0002 (owned by a peer unit) seeds the full
-- IANA option catalog. The default-VRF seed (section 12) is kept.

BEGIN;

-- =============================================================================
-- 1. Core Infrastructure & Authentication
-- =============================================================================

CREATE TABLE users (
  id BIGSERIAL PRIMARY KEY,
  username VARCHAR(255) UNIQUE NOT NULL,
  email VARCHAR(255) UNIQUE NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  role VARCHAR(20) NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'operator', 'viewer')),
  description TEXT,
  is_active BOOLEAN DEFAULT true,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_is_active ON users(is_active);

-- API tokens (long-lived, static). Per amendment A14, token_hash stores the
-- SHA-256 hex digest of the raw token (not bcrypt, which cannot support
-- deterministic lookup).
CREATE TABLE api_tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash VARCHAR(255) UNIQUE NOT NULL,  -- SHA-256 hex of raw token
  description VARCHAR(255),
  last_used_at TIMESTAMP WITH TIME ZONE,
  expires_at TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_api_tokens_user_id ON api_tokens(user_id);
CREATE INDEX idx_api_tokens_token_hash ON api_tokens(token_hash);
CREATE INDEX idx_api_tokens_expires_at ON api_tokens(expires_at);

-- Audit log for compliance and debugging
CREATE TABLE audit_log (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  action VARCHAR(50) NOT NULL,  -- 'create', 'update', 'delete', 'lease_assign', etc.
  entity_type VARCHAR(50) NOT NULL,  -- 'scope', 'pool', 'reservation', 'lease', etc.
  entity_id VARCHAR(255) NOT NULL,  -- UUID or ID
  entity_name VARCHAR(255),
  changes JSONB,  -- {"field": {"old": value, "new": value}, ...}
  ip_address INET,
  user_agent VARCHAR(255),
  timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  description TEXT
);

CREATE INDEX idx_audit_log_timestamp ON audit_log(timestamp DESC);
CREATE INDEX idx_audit_log_user_id ON audit_log(user_id);
CREATE INDEX idx_audit_log_entity ON audit_log(entity_type, entity_id);

-- =============================================================================
-- 2. Clustering & High Availability
-- =============================================================================

CREATE TABLE clusters (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) UNIQUE NOT NULL,
  description TEXT,
  vip_address INET,
  vip_interface VARCHAR(255),
  vip_virtual_mac MACADDR,
  heartbeat_interval_ms INTEGER DEFAULT 1000,
  heartbeat_ttl_ms INTEGER DEFAULT 3000,
  election_interval_ms INTEGER DEFAULT 2000,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_clusters_name ON clusters(name);

CREATE TABLE cluster_servers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  node_name VARCHAR(255) NOT NULL,
  node_id INTEGER NOT NULL,  -- Tie-breaker: lower wins election
  api_address VARCHAR(255) NOT NULL,
  dhcp_address VARCHAR(255) NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(cluster_id, node_name),
  UNIQUE(cluster_id, node_id)
);

CREATE INDEX idx_cluster_servers_cluster_id ON cluster_servers(cluster_id);

-- Heartbeat table for HA election and VIP assignment
CREATE TABLE cluster_heartbeat (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  server_id UUID NOT NULL REFERENCES cluster_servers(id) ON DELETE CASCADE,
  heartbeat_time TIMESTAMP WITH TIME ZONE NOT NULL,
  role VARCHAR(50) NOT NULL DEFAULT 'SECONDARY',  -- 'PRIMARY' or 'SECONDARY'
  vip_assigned BOOLEAN NOT NULL DEFAULT false,
  lease_version INTEGER NOT NULL DEFAULT 0,  -- Incremented by primary on config change
  last_config_version INTEGER NOT NULL DEFAULT 0,  -- Version applied by this server
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(cluster_id, server_id)
);

CREATE INDEX idx_cluster_heartbeat_time ON cluster_heartbeat(cluster_id, heartbeat_time DESC);

-- Election log for debugging failovers
CREATE TABLE cluster_election_log (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  elected_server_id UUID NOT NULL REFERENCES cluster_servers(id),
  elected_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  reason TEXT
);

CREATE INDEX idx_election_log_cluster ON cluster_election_log(cluster_id, elected_at DESC);

-- Schema migrations tracking. IF NOT EXISTS because internal/db/migrate.go
-- bootstraps this same table before it can query it, in case this migration
-- (0001) has not run yet.
CREATE TABLE IF NOT EXISTS schema_migrations (
  id BIGSERIAL PRIMARY KEY,
  version BIGINT UNIQUE NOT NULL,
  name VARCHAR(255) NOT NULL,
  executed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  execution_time_ms INTEGER
);

CREATE INDEX IF NOT EXISTS idx_schema_migrations_version ON schema_migrations(version);

-- =============================================================================
-- 3. VRF (Virtual Routing Forwarding)
-- =============================================================================

CREATE TABLE vrfs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) UNIQUE NOT NULL,
  route_distinguisher VARCHAR(21),  -- Format: ASN:value or IP:value
  description TEXT,
  is_default BOOLEAN DEFAULT false,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_vrfs_name ON vrfs(name);
CREATE INDEX idx_vrfs_is_default ON vrfs(is_default);

-- =============================================================================
-- 4. IPAM (IP Address Management) - Prefix Hierarchy
-- =============================================================================

-- btree_gist registers the operator classes (including inet_ops) that let a
-- GiST index/exclusion constraint cover an equality column (vrf_id) plus a
-- containment/overlap column (prefix) together.
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE ipam_prefixes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vrf_id UUID NOT NULL REFERENCES vrfs(id) ON DELETE CASCADE,
  prefix CIDR NOT NULL,
  parent_id UUID REFERENCES ipam_prefixes(id) ON DELETE CASCADE,  -- Hierarchy
  prefix_type VARCHAR(50) NOT NULL,  -- 'aggregate', 'container', 'subnet'
  name VARCHAR(255),
  description TEXT,
  utilization_percentage INTEGER,  -- Cached from stats
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT valid_type CHECK (prefix_type IN ('aggregate', 'container', 'subnet')),
  CONSTRAINT cidr_valid CHECK (prefix = network(prefix))
);

-- Overlapping constraint via EXCLUSION (no overlaps within VRF at subnet
-- level). Amendment A5.2: opclass named explicitly as inet_ops.
ALTER TABLE ipam_prefixes ADD CONSTRAINT no_overlapping_subnets
EXCLUDE USING gist (
  vrf_id WITH =,
  prefix inet_ops WITH &&
) WHERE (prefix_type = 'subnet');

CREATE INDEX idx_ipam_prefixes_vrf_id ON ipam_prefixes(vrf_id);
CREATE INDEX idx_ipam_prefixes_parent_id ON ipam_prefixes(parent_id);
CREATE INDEX idx_ipam_prefixes_prefix_gist ON ipam_prefixes USING gist (prefix inet_ops);
CREATE INDEX idx_ipam_prefixes_type ON ipam_prefixes(prefix_type);

-- Server interface configuration (interface -> VRF mapping)
CREATE TABLE server_interfaces (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  server_id UUID NOT NULL REFERENCES cluster_servers(id) ON DELETE CASCADE,
  interface_name VARCHAR(255) NOT NULL,
  mac_address MACADDR NOT NULL,
  ip_address INET NOT NULL,
  vrf_id UUID NOT NULL REFERENCES vrfs(id) ON DELETE CASCADE,
  description TEXT,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(server_id, interface_name),
  UNIQUE(server_id, vrf_id)  -- One listener per VRF per server
);

CREATE INDEX idx_server_interfaces_server_id ON server_interfaces(server_id);
CREATE INDEX idx_server_interfaces_vrf_id ON server_interfaces(vrf_id);

-- =============================================================================
-- 5. DHCP Scopes & Pools
-- =============================================================================

CREATE TABLE dhcp_scopes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vrf_id UUID NOT NULL REFERENCES vrfs(id) ON DELETE CASCADE,
  cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  ipam_prefix_id UUID NOT NULL REFERENCES ipam_prefixes(id) ON DELETE CASCADE,
  name VARCHAR(255) NOT NULL,
  description TEXT,
  network CIDR NOT NULL,  -- Cached from ipam_prefix
  is_enabled BOOLEAN DEFAULT true,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  UNIQUE(vrf_id, cluster_id, ipam_prefix_id),
  CONSTRAINT cidr_valid CHECK (network = network(network))
);

CREATE INDEX idx_dhcp_scopes_vrf_id ON dhcp_scopes(vrf_id);
CREATE INDEX idx_dhcp_scopes_cluster_id ON dhcp_scopes(cluster_id);
CREATE INDEX idx_dhcp_scopes_ipam_prefix_id ON dhcp_scopes(ipam_prefix_id);
CREATE INDEX idx_dhcp_scopes_is_enabled ON dhcp_scopes(is_enabled);

CREATE TABLE dhcp_pools (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope_id UUID NOT NULL REFERENCES dhcp_scopes(id) ON DELETE CASCADE,
  name VARCHAR(255) NOT NULL,
  description TEXT,
  range_start INET NOT NULL,
  range_end INET NOT NULL,
  is_enabled BOOLEAN DEFAULT true,
  priority INTEGER DEFAULT 0,  -- Higher = preferred; used for allocation order
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  UNIQUE(scope_id, name),
  CONSTRAINT range_valid CHECK (range_start <= range_end)
);

CREATE INDEX idx_dhcp_pools_scope_id ON dhcp_pools(scope_id);
CREATE INDEX idx_dhcp_pools_is_enabled ON dhcp_pools(is_enabled);
CREATE INDEX idx_dhcp_pools_priority ON dhcp_pools(priority DESC);

-- Trigger to validate pool ranges are within scope
CREATE FUNCTION validate_pool_range()
RETURNS TRIGGER AS $$
DECLARE
  scope_network CIDR;
BEGIN
  SELECT network INTO scope_network FROM dhcp_scopes WHERE id = NEW.scope_id;

  IF scope_network IS NULL THEN
    RAISE EXCEPTION 'Scope % not found', NEW.scope_id;
  END IF;

  IF NOT (NEW.range_start::cidr << scope_network AND
          NEW.range_end::cidr << scope_network) THEN
    RAISE EXCEPTION 'Pool range [%, %] outside scope %',
      NEW.range_start, NEW.range_end, scope_network;
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_validate_pool_range
BEFORE INSERT OR UPDATE ON dhcp_pools
FOR EACH ROW
EXECUTE FUNCTION validate_pool_range();

-- =============================================================================
-- 6. Reservations & Leases
-- =============================================================================

CREATE TABLE dhcp_reservations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pool_id UUID NOT NULL REFERENCES dhcp_pools(id) ON DELETE CASCADE,
  hostname VARCHAR(255),
  description TEXT,
  reserved_ip INET NOT NULL,
  binding_key VARCHAR(255) NOT NULL,  -- 'mac:00:11:22:33:44:55' or 'duid:...'
  binding_type VARCHAR(50) NOT NULL,  -- 'mac', 'client_id', 'hostname'
  is_enabled BOOLEAN DEFAULT true,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  UNIQUE(pool_id, binding_key),
  CONSTRAINT valid_binding_type CHECK (binding_type IN ('mac', 'client_id', 'hostname'))
);

CREATE INDEX idx_dhcp_reservations_pool_id ON dhcp_reservations(pool_id);
CREATE INDEX idx_dhcp_reservations_binding_key ON dhcp_reservations(binding_key);
CREATE INDEX idx_dhcp_reservations_reserved_ip ON dhcp_reservations(reserved_ip);
CREATE INDEX idx_dhcp_reservations_is_enabled ON dhcp_reservations(is_enabled);

-- Active DHCP leases. Amendment A5.1: binding_server_id drops NOT NULL —
-- it has ON DELETE SET NULL, which is contradictory with NOT NULL.
CREATE TABLE dhcp_leases (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pool_id UUID NOT NULL REFERENCES dhcp_pools(id) ON DELETE CASCADE,
  ip_address INET NOT NULL,
  client_mac MACADDR,
  client_id BYTEA,  -- Raw DHCP client ID
  client_hostname VARCHAR(255),
  binding_state VARCHAR(50) DEFAULT 'active',  -- 'active', 'expired', 'released', 'abandoned'
  binding_server_id UUID REFERENCES cluster_servers(id) ON DELETE SET NULL,
  lease_start TIMESTAMP WITH TIME ZONE NOT NULL,
  lease_end TIMESTAMP WITH TIME ZONE NOT NULL,
  renewal_time TIMESTAMP WITH TIME ZONE,
  rebinding_time TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(pool_id, ip_address),
  CONSTRAINT state_valid CHECK (binding_state IN ('active', 'expired', 'released', 'abandoned')),
  CONSTRAINT times_valid CHECK (lease_start <= renewal_time AND renewal_time <= rebinding_time AND rebinding_time <= lease_end)
);

CREATE INDEX idx_dhcp_leases_pool_id_state ON dhcp_leases(pool_id, binding_state);
CREATE INDEX idx_dhcp_leases_ip_address ON dhcp_leases(ip_address);
CREATE INDEX idx_dhcp_leases_client_mac ON dhcp_leases(client_mac) WHERE client_mac IS NOT NULL;
CREATE INDEX idx_dhcp_leases_client_id ON dhcp_leases(client_id) WHERE client_id IS NOT NULL;
CREATE INDEX idx_dhcp_leases_lease_end ON dhcp_leases(lease_end) WHERE binding_state IN ('active', 'released');
CREATE INDEX idx_dhcp_leases_binding_server ON dhcp_leases(binding_server_id);

-- Incremental active lease count cache (for fast utilization queries)
CREATE TABLE stats_active_lease_count (
  id BIGSERIAL PRIMARY KEY,
  pool_id UUID NOT NULL UNIQUE REFERENCES dhcp_pools(id) ON DELETE CASCADE,
  active_count BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_active_lease_count_pool ON stats_active_lease_count(pool_id);

-- =============================================================================
-- 7. DHCP Options (Catalog & Values)
-- =============================================================================

-- Amendment A5.4: value_type CHECK gains 'fqdn' and 'route_list' (option
-- 121, classless static routes).
CREATE TABLE dhcp_option_catalog (
  option_code SMALLINT PRIMARY KEY,
  name VARCHAR(255) NOT NULL UNIQUE,
  value_type VARCHAR(50) NOT NULL,  -- 'string', 'uint8', 'uint16', 'uint32', 'ip', 'ip_list', 'bytes', 'boolean', 'fqdn', 'route_list'
  description TEXT NOT NULL,
  iana_reference VARCHAR(255),  -- e.g. "RFC 2132"
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT valid_type CHECK (value_type IN ('string', 'uint8', 'uint16', 'uint32', 'ip', 'ip_list', 'bytes', 'boolean', 'fqdn', 'route_list')),
  CONSTRAINT valid_code CHECK (option_code >= 1 AND option_code <= 254)
);

CREATE INDEX idx_dhcp_option_catalog_name ON dhcp_option_catalog(name);

-- Option values at different scopes (inheritance)
CREATE TABLE dhcp_option_values (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  option_code SMALLINT NOT NULL REFERENCES dhcp_option_catalog(option_code) ON DELETE CASCADE,
  scope_level VARCHAR(50) NOT NULL,  -- 'global', 'template', 'scope', 'pool', 'reservation'
  scope_entity_id UUID NOT NULL,  -- ID of entity at this level
  value TEXT NOT NULL,
  is_enabled BOOLEAN DEFAULT true,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  UNIQUE(option_code, scope_level, scope_entity_id),
  CONSTRAINT valid_level CHECK (scope_level IN ('global', 'template', 'scope', 'pool', 'reservation'))
);

CREATE INDEX idx_dhcp_option_values_option_code ON dhcp_option_values(option_code);
CREATE INDEX idx_dhcp_option_values_scope ON dhcp_option_values(scope_level, scope_entity_id);

-- =============================================================================
-- 8. Templates (Reusable DHCP Settings)
-- =============================================================================

CREATE TABLE dhcp_templates (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) UNIQUE NOT NULL,
  description TEXT,
  is_enabled BOOLEAN DEFAULT true,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_dhcp_templates_name ON dhcp_templates(name);

-- Template -> Scope associations (with priority for multi-template scopes)
CREATE TABLE dhcp_template_scopes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  template_id UUID NOT NULL REFERENCES dhcp_templates(id) ON DELETE CASCADE,
  scope_id UUID NOT NULL REFERENCES dhcp_scopes(id) ON DELETE CASCADE,
  priority INTEGER DEFAULT 0,  -- Higher = checked first in inheritance
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(template_id, scope_id)
);

CREATE INDEX idx_dhcp_template_scopes_template_id ON dhcp_template_scopes(template_id);
CREATE INDEX idx_dhcp_template_scopes_scope_id ON dhcp_template_scopes(scope_id);

-- Template -> Pool associations
CREATE TABLE dhcp_template_pools (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  template_id UUID NOT NULL REFERENCES dhcp_templates(id) ON DELETE CASCADE,
  pool_id UUID NOT NULL REFERENCES dhcp_pools(id) ON DELETE CASCADE,
  priority INTEGER DEFAULT 0,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(template_id, pool_id)
);

CREATE INDEX idx_dhcp_template_pools_template_id ON dhcp_template_pools(template_id);
CREATE INDEX idx_dhcp_template_pools_pool_id ON dhcp_template_pools(pool_id);

-- =============================================================================
-- 9. Tags & Labels (Entity Tagging)
-- =============================================================================

CREATE TABLE tags (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) UNIQUE NOT NULL,
  color VARCHAR(7),  -- Hex color: #RRGGBB
  description TEXT,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_tags_name ON tags(name);

-- Entity tagging (many-to-many)
CREATE TABLE entity_tags (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  entity_type VARCHAR(50) NOT NULL,  -- 'vrf', 'scope', 'pool', 'reservation', 'server', 'cluster'
  entity_id UUID NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(tag_id, entity_type, entity_id)
);

CREATE INDEX idx_entity_tags_entity ON entity_tags(entity_type, entity_id);
CREATE INDEX idx_entity_tags_tag_id ON entity_tags(tag_id);

-- =============================================================================
-- 10. Statistics & Monitoring
-- =============================================================================

-- Utilization samples (one per minute, typically)
CREATE TABLE stats_utilization_samples (
  id BIGSERIAL PRIMARY KEY,
  sample_timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
  entity_type VARCHAR(50) NOT NULL,  -- 'scope', 'pool', 'vrf'
  entity_id UUID NOT NULL,
  total_addresses BIGINT NOT NULL,
  used_addresses BIGINT NOT NULL,
  reserved_addresses BIGINT NOT NULL,
  available_addresses BIGINT NOT NULL,
  utilization_percentage NUMERIC(5, 2) NOT NULL
);

CREATE INDEX idx_stats_utilization_entity ON stats_utilization_samples(entity_type, entity_id);
CREATE INDEX idx_stats_utilization_timestamp ON stats_utilization_samples(sample_timestamp DESC);

-- Hourly rollup (for dashboard trends)
CREATE TABLE stats_utilization_hourly (
  id BIGSERIAL PRIMARY KEY,
  sample_hour TIMESTAMP WITH TIME ZONE NOT NULL,
  entity_type VARCHAR(50) NOT NULL,
  entity_id UUID NOT NULL,
  total_addresses BIGINT NOT NULL,
  used_addresses_avg NUMERIC(10, 2),
  used_addresses_max BIGINT,
  used_addresses_min BIGINT,
  utilization_percentage_avg NUMERIC(5, 2),
  utilization_percentage_max NUMERIC(5, 2),
  utilization_percentage_min NUMERIC(5, 2),
  UNIQUE(sample_hour, entity_type, entity_id)
);

CREATE INDEX idx_stats_utilization_hourly_entity ON stats_utilization_hourly(entity_type, entity_id);
CREATE INDEX idx_stats_utilization_hourly_hour ON stats_utilization_hourly(sample_hour DESC);

-- DHCP transaction statistics (per scope, daily)
CREATE TABLE stats_dhcp_transactions (
  id BIGSERIAL PRIMARY KEY,
  stat_date DATE NOT NULL,
  scope_id UUID NOT NULL REFERENCES dhcp_scopes(id) ON DELETE CASCADE,
  discover_count BIGINT DEFAULT 0,
  offer_count BIGINT DEFAULT 0,
  request_count BIGINT DEFAULT 0,
  ack_count BIGINT DEFAULT 0,
  nak_count BIGINT DEFAULT 0,
  decline_count BIGINT DEFAULT 0,
  release_count BIGINT DEFAULT 0,
  no_leases_count BIGINT DEFAULT 0,
  UNIQUE(stat_date, scope_id)
);

CREATE INDEX idx_stats_dhcp_scope_date ON stats_dhcp_transactions(scope_id, stat_date DESC);

-- =============================================================================
-- 11. Seed: Default VRF (bootstrap)
-- =============================================================================
-- The IANA DHCPv4 option catalog seed lives in migration 0002 (owned by a
-- peer unit), not here.

INSERT INTO vrfs (name, is_default, description)
VALUES ('default', true, 'Default virtual routing forwarding domain')
ON CONFLICT (name) DO NOTHING;

COMMIT;
