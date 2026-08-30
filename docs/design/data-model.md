# gdhcp Data Model Design

## Overview

This document specifies the complete PostgreSQL data model for gdhcp, a high-availability DHCP server with integrated IPAM. The model supports:

- **VRF isolation**: Multiple virtual routing domains with overlapping CIDR support
- **IPAM tree**: Hierarchical prefix management (aggregates/containers) with proper CIDR nesting
- **DHCP scopes & pools**: Subnets served by clusters with address ranges
- **Reservations & leases**: Client-to-IP bindings with fast concurrent allocation
- **Option inheritance**: Global → template → scope → pool → reservation hierarchy
- **HA clustering**: Multiple servers per cluster with VIP failover
- **Audit & auth**: User accounts, API tokens, comprehensive change logging
- **Observability**: Utilization tracking, stats sampling for historical dashboards

## Design Principles

1. **Concurrency-safe**: Leases use SELECT ... FOR UPDATE SKIP LOCKED; no pessimistic locking on stats
2. **Hierarchical options**: Inheritance order is clearly defined; cascading updates via triggers or careful transactions
3. **Scope isolation**: VRF constraints prevent CIDR overlaps within a VRF; different VRFs allow identical CIDRs
4. **Audit trail**: All user-visible changes logged with user, timestamp, change type, before/after values
5. **CIDR containment**: PostgreSQL inet/cidr types with proper indexing (gist for containment queries)
6. **Stats approach**: **Rolled-up samples table + computed on read** — dashboard shows historical trends from samples; current utilization computed at read time for accuracy

## Table Definitions (DDL)

### 1. Core Infrastructure

#### `users`
```sql
CREATE TABLE users (
  id BIGSERIAL PRIMARY KEY,
  username VARCHAR(255) UNIQUE NOT NULL,
  email VARCHAR(255) UNIQUE NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  description TEXT,
  is_active BOOLEAN DEFAULT true,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
```

#### `api_tokens`
```sql
CREATE TABLE api_tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash VARCHAR(255) UNIQUE NOT NULL,
  description VARCHAR(255),
  last_used_at TIMESTAMP WITH TIME ZONE,
  expires_at TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_api_tokens_user_id ON api_tokens(user_id);
CREATE INDEX idx_api_tokens_expires_at ON api_tokens(expires_at);
```

#### `audit_log`
```sql
CREATE TABLE audit_log (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  action VARCHAR(50) NOT NULL, -- 'create', 'update', 'delete', 'lease_assign', etc.
  entity_type VARCHAR(50) NOT NULL, -- 'scope', 'pool', 'reservation', 'lease', etc.
  entity_id VARCHAR(255) NOT NULL, -- UUID or numeric ID
  entity_name VARCHAR(255),
  changes JSONB, -- {"field": {"old": value, "new": value}, ...}
  ip_address INET,
  user_agent VARCHAR(255),
  timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  description TEXT
);

CREATE INDEX idx_audit_log_timestamp ON audit_log(timestamp DESC);
CREATE INDEX idx_audit_log_user_id ON audit_log(user_id);
CREATE INDEX idx_audit_log_entity_type_id ON audit_log(entity_type, entity_id);
```

### 2. Clustering & High Availability

#### `clusters`
```sql
CREATE TABLE clusters (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) UNIQUE NOT NULL,
  description TEXT,
  vip_address INET,
  vip_interface VARCHAR(255),
  vip_virtual_mac MACADDR,
  heartbeat_interval_ms INTEGER DEFAULT 1000,
  heartbeat_timeout_ms INTEGER DEFAULT 3000,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_clusters_name ON clusters(name);
```

#### `servers`
```sql
CREATE TABLE servers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  hostname VARCHAR(255) NOT NULL,
  ip_address INET NOT NULL,
  port INTEGER DEFAULT 67,
  role VARCHAR(50) NOT NULL, -- 'primary', 'secondary'
  heartbeat_state VARCHAR(50) DEFAULT 'unknown', -- 'healthy', 'unhealthy', 'unknown'
  last_heartbeat TIMESTAMP WITH TIME ZONE,
  description TEXT,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  UNIQUE(cluster_id, hostname),
  CONSTRAINT valid_role CHECK (role IN ('primary', 'secondary'))
);

CREATE INDEX idx_servers_cluster_id ON servers(cluster_id);
CREATE INDEX idx_servers_ip_address ON servers(ip_address);
```

#### `server_interfaces`
```sql
CREATE TABLE server_interfaces (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  interface_name VARCHAR(255) NOT NULL,
  mac_address MACADDR NOT NULL,
  ip_address INET NOT NULL,
  vrf_id UUID NOT NULL REFERENCES vrfs(id) ON DELETE CASCADE,
  description TEXT,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(server_id, interface_name),
  UNIQUE(server_id, vrf_id) -- one listener per VRF per server
);

CREATE INDEX idx_server_interfaces_server_id ON server_interfaces(server_id);
CREATE INDEX idx_server_interfaces_vrf_id ON server_interfaces(vrf_id);
```

### 3. VRF & IPAM

#### `vrfs`
```sql
CREATE TABLE vrfs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) UNIQUE NOT NULL,
  route_distinguisher VARCHAR(21), -- RD format: ASN:value or IP:value
  description TEXT,
  is_default BOOLEAN DEFAULT false,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_vrfs_name ON vrfs(name);
CREATE INDEX idx_vrfs_is_default ON vrfs(is_default);
```

#### `ipam_prefixes`
```sql
CREATE TABLE ipam_prefixes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vrf_id UUID NOT NULL REFERENCES vrfs(id) ON DELETE CASCADE,
  prefix CIDR NOT NULL,
  parent_id UUID REFERENCES ipam_prefixes(id) ON DELETE CASCADE, -- hierarchy
  prefix_type VARCHAR(50) NOT NULL, -- 'aggregate', 'container', 'subnet'
  name VARCHAR(255),
  description TEXT,
  is_pool BOOLEAN DEFAULT false, -- can this prefix be used as a pool?
  utilization_percentage INTEGER, -- cached from stats
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT valid_type CHECK (prefix_type IN ('aggregate', 'container', 'subnet')),
  CONSTRAINT cidr_valid CHECK (prefix = network(prefix))
);

-- Exclusion constraint: no overlapping prefixes within same VRF at same level
CREATE UNIQUE INDEX idx_ipam_prefixes_no_overlap 
  ON ipam_prefixes(vrf_id, prefix) 
  WHERE prefix_type = 'subnet';

CREATE INDEX idx_ipam_prefixes_vrf_id ON ipam_prefixes(vrf_id);
CREATE INDEX idx_ipam_prefixes_parent_id ON ipam_prefixes(parent_id);
CREATE INDEX idx_ipam_prefixes_prefix_gist ON ipam_prefixes USING GIST(prefix);
```

### 4. DHCP Scopes & Pools

#### `dhcp_scopes`
```sql
CREATE TABLE dhcp_scopes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vrf_id UUID NOT NULL REFERENCES vrfs(id) ON DELETE CASCADE,
  cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  ipam_prefix_id UUID NOT NULL REFERENCES ipam_prefixes(id) ON DELETE CASCADE,
  name VARCHAR(255) NOT NULL,
  description TEXT,
  network CIDR NOT NULL, -- cached from ipam_prefix
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
```

#### `dhcp_pools`
```sql
CREATE TABLE dhcp_pools (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope_id UUID NOT NULL REFERENCES dhcp_scopes(id) ON DELETE CASCADE,
  name VARCHAR(255) NOT NULL,
  description TEXT,
  range_start INET NOT NULL,
  range_end INET NOT NULL,
  is_enabled BOOLEAN DEFAULT true,
  priority INTEGER DEFAULT 0, -- higher = preferred; used for allocation order
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  UNIQUE(scope_id, name),
  CONSTRAINT range_valid CHECK (range_start <= range_end),
  CONSTRAINT range_in_scope CHECK (range_start::cidr << (SELECT network FROM dhcp_scopes WHERE id = scope_id)),
  CONSTRAINT range_in_scope_end CHECK (range_end::cidr << (SELECT network FROM dhcp_scopes WHERE id = scope_id))
);

CREATE INDEX idx_dhcp_pools_scope_id ON dhcp_pools(scope_id);
CREATE INDEX idx_dhcp_pools_is_enabled ON dhcp_pools(is_enabled);
CREATE INDEX idx_dhcp_pools_priority ON dhcp_pools(priority DESC);
```

### 5. Reservations & Leases

#### `dhcp_reservations`
```sql
CREATE TABLE dhcp_reservations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pool_id UUID NOT NULL REFERENCES dhcp_pools(id) ON DELETE CASCADE,
  hostname VARCHAR(255),
  description TEXT,
  reserved_ip INET NOT NULL,
  binding_key VARCHAR(255) NOT NULL, -- MAC address or client-id; format: 'mac:00:11:22:33:44:55' or 'duid:...'
  binding_type VARCHAR(50) NOT NULL, -- 'mac', 'client_id', 'hostname'
  is_enabled BOOLEAN DEFAULT true,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  UNIQUE(pool_id, binding_key),
  CONSTRAINT valid_binding_type CHECK (binding_type IN ('mac', 'client_id', 'hostname')),
  CONSTRAINT ip_in_pool CHECK (reserved_ip >= (SELECT range_start FROM dhcp_pools WHERE id = pool_id) 
                                AND reserved_ip <= (SELECT range_end FROM dhcp_pools WHERE id = pool_id))
);

CREATE INDEX idx_dhcp_reservations_pool_id ON dhcp_reservations(pool_id);
CREATE INDEX idx_dhcp_reservations_binding_key ON dhcp_reservations(binding_key);
CREATE INDEX idx_dhcp_reservations_reserved_ip ON dhcp_reservations(reserved_ip);
CREATE INDEX idx_dhcp_reservations_is_enabled ON dhcp_reservations(is_enabled);
```

#### `dhcp_leases`
```sql
CREATE TABLE dhcp_leases (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pool_id UUID NOT NULL REFERENCES dhcp_pools(id) ON DELETE CASCADE,
  ip_address INET NOT NULL,
  client_mac MACADDR,
  client_id BYTEA, -- raw DHCP client ID
  client_hostname VARCHAR(255),
  binding_state VARCHAR(50) DEFAULT 'active', -- 'active', 'expired', 'released', 'abandoned'
  binding_server_id UUID NOT NULL REFERENCES servers(id) ON DELETE SET NULL, -- which server allocated
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

-- Indexes for DORA allocation (fast "find available IP in range")
CREATE INDEX idx_dhcp_leases_pool_id_state ON dhcp_leases(pool_id, binding_state);
CREATE INDEX idx_dhcp_leases_ip_address ON dhcp_leases(ip_address);
CREATE INDEX idx_dhcp_leases_client_mac ON dhcp_leases(client_mac) WHERE client_mac IS NOT NULL;
CREATE INDEX idx_dhcp_leases_client_id ON dhcp_leases(client_id) WHERE client_id IS NOT NULL;
CREATE INDEX idx_dhcp_leases_lease_end ON dhcp_leases(lease_end) WHERE binding_state IN ('active', 'released');
```

### 6. DHCP Options

#### `dhcp_option_catalog`
```sql
CREATE TABLE dhcp_option_catalog (
  option_code SMALLINT PRIMARY KEY,
  name VARCHAR(255) NOT NULL UNIQUE,
  value_type VARCHAR(50) NOT NULL, -- 'string', 'uint8', 'uint16', 'uint32', 'ip', 'ip_list', 'bytes', 'boolean'
  description TEXT NOT NULL,
  iana_reference VARCHAR(255), -- e.g., "RFC 2132"
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT valid_type CHECK (value_type IN ('string', 'uint8', 'uint16', 'uint32', 'ip', 'ip_list', 'bytes', 'boolean')),
  CONSTRAINT valid_code CHECK (option_code >= 1 AND option_code <= 254)
);

CREATE INDEX idx_dhcp_option_catalog_name ON dhcp_option_catalog(name);
```

#### `dhcp_option_values`
```sql
CREATE TABLE dhcp_option_values (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  option_code SMALLINT NOT NULL REFERENCES dhcp_option_catalog(option_code) ON DELETE CASCADE,
  scope_level VARCHAR(50) NOT NULL, -- 'global', 'template', 'scope', 'pool', 'reservation'
  scope_entity_id UUID NOT NULL, -- ID of the entity at this level
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
```

### 7. Templates

#### `dhcp_templates`
```sql
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
```

#### `dhcp_template_scopes`
```sql
CREATE TABLE dhcp_template_scopes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  template_id UUID NOT NULL REFERENCES dhcp_templates(id) ON DELETE CASCADE,
  scope_id UUID NOT NULL REFERENCES dhcp_scopes(id) ON DELETE CASCADE,
  priority INTEGER DEFAULT 0,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(template_id, scope_id)
);

CREATE INDEX idx_dhcp_template_scopes_template_id ON dhcp_template_scopes(template_id);
CREATE INDEX idx_dhcp_template_scopes_scope_id ON dhcp_template_scopes(scope_id);
```

#### `dhcp_template_pools`
```sql
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
```

### 8. Tags, Labels, and Metadata

#### `tags`
```sql
CREATE TABLE tags (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) UNIQUE NOT NULL,
  color VARCHAR(7), -- hex color: #RRGGBB
  description TEXT,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_tags_name ON tags(name);
```

#### `entity_tags`
```sql
CREATE TABLE entity_tags (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  entity_type VARCHAR(50) NOT NULL, -- 'vrf', 'scope', 'pool', 'reservation', 'server', 'cluster'
  entity_id UUID NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(tag_id, entity_type, entity_id)
);

CREATE INDEX idx_entity_tags_entity ON entity_tags(entity_type, entity_id);
CREATE INDEX idx_entity_tags_tag_id ON entity_tags(tag_id);
```

### 9. Utilization & Stats

#### `stats_utilization_samples`
```sql
CREATE TABLE stats_utilization_samples (
  id BIGSERIAL PRIMARY KEY,
  sample_timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
  entity_type VARCHAR(50) NOT NULL, -- 'scope', 'pool', 'vrf'
  entity_id UUID NOT NULL,
  total_addresses BIGINT NOT NULL,
  used_addresses BIGINT NOT NULL, -- currently leased (active + released but not yet expired)
  reserved_addresses BIGINT NOT NULL,
  available_addresses BIGINT NOT NULL,
  utilization_percentage NUMERIC(5, 2) NOT NULL
);

-- Rollup for dashboard: keep samples for 90 days; older data aggregated hourly
CREATE INDEX idx_stats_utilization_entity ON stats_utilization_samples(entity_type, entity_id);
CREATE INDEX idx_stats_utilization_timestamp ON stats_utilization_samples(sample_timestamp DESC);
```

#### `stats_utilization_hourly`
```sql
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
```

## Inheritance & Option Resolution Order

When a DHCP request arrives:

1. Start at the pool level (via the lease)
2. Check for option values at: **reservation > pool > scope > template > global**
3. Use first defined value; skip NULL/disabled options
4. Merge with option catalog metadata (name, description, type)

Implementation note: This is resolved at **request time** in the DHCP server logic, not at DB level. Options are inherited by traversing the entity hierarchy and querying `dhcp_option_values` in precedence order.

## Concurrency & Locking Strategy

### Lease Allocation (DORA)

```sql
-- In a transaction with SERIALIZABLE isolation or explicit locking:
SELECT id, ip_address FROM dhcp_leases
WHERE pool_id = $pool_id
  AND binding_state IN ('released', 'expired')
  AND lease_end < NOW()
ORDER BY lease_end ASC
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- If no free lease found, allocate new:
INSERT INTO dhcp_leases (pool_id, ip_address, ...)
ON CONFLICT (pool_id, ip_address) DO UPDATE
SET binding_state = 'active', lease_start = NOW(), ...
WHERE binding_state IN ('released', 'expired');
```

This allows multiple servers in the cluster to safely allocate leases concurrently. SKIP LOCKED prevents blocking on contended rows; ON CONFLICT ensures idempotent allocation.

### Stats Updates

Stats are collected asynchronously (no locking) and written to `stats_utilization_samples` by a background job every 60 seconds (configurable). This avoids contention on frequently-updated stats rows.

## Go Model Struct Naming Convention

All Go model structs use **UpperCamelCase** (PascalCase). Database tables and columns use **lowercase_with_underscores**.

Example mappings:

```go
// User represents a system user
type User struct {
  ID            int64
  Username      string
  Email         string
  PasswordHash  string
  Description   *string
  IsActive      bool
  CreatedAt     time.Time
  UpdatedAt     time.Time
  CreatedByUser *User
  UpdatedByUser *User
}

// Cluster represents an HA cluster of DHCP servers
type Cluster struct {
  ID                    uuid.UUID
  Name                  string
  Description           *string
  VIPAddress            *net.IP
  VIPInterface          *string
  VIPVirtualMAC         *net.HardwareAddr
  HeartbeatIntervalMs   int
  HeartbeatTimeoutMs    int
  CreatedAt             time.Time
  UpdatedAt             time.Time
  CreatedByUser         *User
  UpdatedByUser         *User
}

// DHCPScope represents a DHCP-served subnet
type DHCPScope struct {
  ID            uuid.UUID
  VRF           *VRF
  Cluster       *Cluster
  IPAMPrefix    *IPAMPrefix
  Name          string
  Description   *string
  Network       *net.IPNet  // stored as CIDR
  IsEnabled     bool
  CreatedAt     time.Time
  UpdatedAt     time.Time
  CreatedByUser *User
  UpdatedByUser *User
}

// DHCPLease represents an active DHCP lease
type DHCPLease struct {
  ID              uuid.UUID
  Pool            *DHCPPool
  IPAddress       net.IP
  ClientMAC       *net.HardwareAddr
  ClientID        []byte
  ClientHostname  *string
  BindingState    string // 'active', 'expired', 'released', 'abandoned'
  BindingServer   *Server
  LeaseStart      time.Time
  LeaseEnd        time.Time
  RenewalTime     *time.Time
  RebindingTime   *time.Time
  CreatedAt       time.Time
  UpdatedAt       time.Time
}

// DHCPOptionValue represents an option value at a specific scope level
type DHCPOptionValue struct {
  ID             uuid.UUID
  OptionCode     int16
  ScopeLevel     string // 'global', 'template', 'scope', 'pool', 'reservation'
  ScopeEntityID  uuid.UUID
  Value          string
  IsEnabled      bool
  CreatedAt      time.Time
  UpdatedAt      time.Time
  CreatedByUser  *User
  UpdatedByUser  *User
}
```

**Pointer semantics**: Use `*T` for optional fields (nullable in DB); use value types for required fields.

## Migration Tooling

### Approach: Embedded SQL Migrations at Startup

**Strategy**: Migrations are versioned SQL files embedded in the Go binary using `go:embed`. On application startup, the DHCP server:

1. Reads the migrations from the embedded filesystem
2. Queries `schema_migrations` table to see which migrations have run
3. Executes any pending migrations in order
4. Updates `schema_migrations` with the run version and timestamp

**Advantages**:
- Single binary; no external migration scripts needed
- Transactional; if a migration fails, transaction rolls back
- Simple audit trail in `schema_migrations`
- Fast startup; idempotent (safe to run multiple times)

### Migration Table

```sql
CREATE TABLE schema_migrations (
  id BIGSERIAL PRIMARY KEY,
  version BIGINT UNIQUE NOT NULL,
  name VARCHAR(255) NOT NULL,
  executed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  execution_time_ms INTEGER
);

CREATE INDEX idx_schema_migrations_version ON schema_migrations(version);
```

### Migration File Structure

Migrations live in `/internal/db/migrations/` with names like:

```
001_initial_schema.sql
002_add_dhcp_tables.sql
003_add_audit_log.sql
004_seed_dhcp_options.sql
...
```

Each file is a single transaction (wrapped in BEGIN/COMMIT by the migration runner).

### Seeding the DHCP Option Catalog

Migration `004_seed_dhcp_options.sql` inserts all IANA DHCP options (1–254) with names, types, and RFC references:

```sql
INSERT INTO dhcp_option_catalog (option_code, name, value_type, description, iana_reference)
VALUES
  (1, 'subnet_mask', 'ip', 'The subnet mask option specifies the client subnet mask.', 'RFC 2132'),
  (3, 'routers', 'ip_list', 'The routers option specifies a list of IP addresses for routers on the client subnet.', 'RFC 2132'),
  (6, 'domain_name_servers', 'ip_list', 'The domain name server option specifies a list of DNS servers.', 'RFC 2132'),
  (15, 'domain_name', 'string', 'This option specifies the domain name that the client should use for DNS resolution.', 'RFC 2132'),
  (26, 'interface_mtu', 'uint16', 'This option specifies the MTU to use for the IP layer.', 'RFC 2132'),
  (28, 'broadcast_address', 'ip', 'This option specifies the broadcast address in use on the client subnet.', 'RFC 2132'),
  (33, 'static_routes', 'ip_list', 'This option specifies static routes that the client should install.', 'RFC 2132'),
  (42, 'ntp_servers', 'ip_list', 'This option specifies NTP servers available to the client.', 'RFC 2132'),
  (51, 'ip_address_lease_time', 'uint32', 'This option specifies the duration of the IP address lease, in seconds.', 'RFC 2132'),
  (58, 'renewal_time', 'uint32', 'This option specifies the time interval from the address assignment until the client enters RENEWING state.', 'RFC 2132'),
  (59, 'rebinding_time', 'uint32', 'This option specifies the time interval from the address assignment until the client enters REBINDING state.', 'RFC 2132'),
  -- ... (253 more options)
  (254, 'class_less_static_route', 'bytes', 'Classless Static Route Option.', 'RFC 3442')
ON CONFLICT DO NOTHING;
```

## Stats Collection & Reporting

### Live Utilization (Computed on Read)

When the API queries scope/pool utilization:

```sql
SELECT 
  scope_id,
  COUNT(DISTINCT p.id) as total_pools,
  COALESCE(SUM((p.range_end::bigint - p.range_start::bigint + 1)), 0) as total_addresses,
  COALESCE(SUM(CASE WHEN dl.binding_state = 'active' THEN 1 ELSE 0 END), 0) as active_leases,
  COALESCE(SUM(CASE WHEN dr.id IS NOT NULL THEN 1 ELSE 0 END), 0) as reserved_count
FROM dhcp_scopes ds
LEFT JOIN dhcp_pools p ON ds.id = p.scope_id
LEFT JOIN dhcp_leases dl ON p.id = dl.pool_id AND dl.lease_end > NOW()
LEFT JOIN dhcp_reservations dr ON p.id = dr.pool_id AND dr.is_enabled = true
WHERE ds.id = $scope_id
GROUP BY ds.id;
```

This ensures dashboard shows real-time utilization without staleness.

### Historical Trends (Rolled-Up Samples)

A background task (goroutine in the Go server) samples utilization every 60 seconds:

1. Query all active scopes/pools
2. Compute utilization percentage
3. Insert into `stats_utilization_samples`
4. Every hour, aggregate samples into `stats_utilization_hourly` (avg/min/max)
5. Delete samples older than 90 days

Dashboards can plot the hourly aggregates to show trends without querying millions of sample rows.

**Justification**: Real-time accuracy for "current utilization" + historical trend visibility without storage bloat or query overhead.

## Constraints & Validation

### CIDR Containment (IPAM)

The constraint `CONSTRAINT cidr_valid CHECK (prefix = network(prefix))` ensures only valid, network-form CIDRs are stored (e.g., 10.0.0.0/24, not 10.0.0.5/24).

The GIST index on `prefix` enables fast containment queries:

```sql
SELECT * FROM ipam_prefixes
WHERE prefix @> $child_prefix
  AND vrf_id = $vrf_id
ORDER BY prefix DESC
LIMIT 1; -- immediate parent
```

### No Overlapping CIDRs Within a VRF at Subnet Level

```sql
CREATE UNIQUE INDEX idx_ipam_prefixes_no_overlap 
  ON ipam_prefixes(vrf_id, prefix) 
  WHERE prefix_type = 'subnet';
```

This prevents two subnets in the same VRF from overlapping. Aggregates and containers may still overlap (they are organizational).

### IP Address Range Validity

```sql
CONSTRAINT range_in_scope CHECK (range_start::cidr << (SELECT network FROM dhcp_scopes WHERE id = scope_id)),
CONSTRAINT range_in_scope_end CHECK (range_end::cidr << (SELECT network FROM dhcp_scopes WHERE id = scope_id))
```

Ensures pool IPs stay within the scope's network. Note: These constraints use a subquery, which is evaluated at insert/update time.

### Lease Time Validity

```sql
CONSTRAINT times_valid CHECK (lease_start <= renewal_time AND renewal_time <= rebinding_time AND rebinding_time <= lease_end)
```

Ensures T1 < T2 < lease-end, matching DHCP RFC timing.

## Indexing Strategy

### Hot Paths

1. **Lease allocation** (DORA)
   - `idx_dhcp_leases_pool_id_state` — find expired/released leases in a pool
   - `idx_dhcp_leases_client_mac` — look up existing lease by MAC
   - `idx_dhcp_leases_client_id` — look up by DHCP client ID

2. **Reservation lookup** (on every DHCP request)
   - `idx_dhcp_reservations_binding_key` — MAC/DUID to IP

3. **IPAM traversal** (on scope creation/editing)
   - `idx_ipam_prefixes_prefix_gist` — containment queries (parent/child hierarchy)
   - `idx_ipam_prefixes_vrf_id` — scoped lookups

4. **Server heartbeat** (frequent updates)
   - `idx_servers_cluster_id` — all servers in a cluster
   - Avoid indexing `last_heartbeat` for write-heavy updates (table scan is often faster for this low cardinality)

5. **Option resolution** (every DHCP request in worst case, but likely cached)
   - `idx_dhcp_option_values_scope` — lookup by level+entity

6. **Audit/compliance queries**
   - `idx_audit_log_timestamp` — dashboards, recent activity
   - `idx_audit_log_entity_type_id` — investigate changes to a specific entity

### Full-Text Search (Optional Future)

For searching prefixes, scopes, servers by name/description:

```sql
ALTER TABLE ipam_prefixes ADD COLUMN search_text TSVECTOR;
CREATE INDEX idx_ipam_prefixes_search ON ipam_prefixes USING GIN(search_text);
-- Trigger to keep search_text in sync on INSERT/UPDATE
```

## Summary Table

| Purpose                          | Table                          | Key Index                      |
|----------------------------------|--------------------------------|--------------------------------|
| User identity & auth             | `users`, `api_tokens`          | `idx_api_tokens_user_id`       |
| Change audit trail               | `audit_log`                    | `idx_audit_log_timestamp`      |
| HA cluster metadata              | `clusters`, `servers`          | `idx_servers_cluster_id`       |
| Interface → VRF binding          | `server_interfaces`            | `idx_server_interfaces_vrf_id` |
| VRF isolation                    | `vrfs`                         | `idx_vrfs_name`                |
| IPAM prefix hierarchy            | `ipam_prefixes`                | `idx_ipam_prefixes_prefix_gist`|
| DHCP subnet binding to cluster   | `dhcp_scopes`                  | `idx_dhcp_scopes_cluster_id`   |
| Address range allocation         | `dhcp_pools`                   | `idx_dhcp_pools_scope_id`      |
| MAC/DUID → IP reservations       | `dhcp_reservations`            | `idx_dhcp_reservations_binding_key` |
| Active/expired DHCP leases       | `dhcp_leases`                  | `idx_dhcp_leases_pool_id_state`|
| IANA option catalog (seeded)     | `dhcp_option_catalog`          | `idx_dhcp_option_catalog_name` |
| Option values (multi-level)      | `dhcp_option_values`           | `idx_dhcp_option_values_scope` |
| Settings bundles                 | `dhcp_templates`, `*_template_*` | `idx_dhcp_template_*_id`      |
| Tags for grouping                | `tags`, `entity_tags`          | `idx_entity_tags_entity`       |
| Utilization trends (sampled)     | `stats_utilization_samples`, `stats_utilization_hourly` | `idx_stats_utilization_entity` |

---

## Implementation Notes

1. **UUID vs BigInt**: Most entities use UUID (easier for distributed systems, less chance of ID leakage). Audit log and stats use BIGSERIAL for sequential write performance and disk efficiency.

2. **Soft deletes**: Not used; rely on ON DELETE CASCADE for referential integrity. If audit trail is needed, the `audit_log` table captures all changes.

3. **Null handling**: Nullable fields (optional descriptions, VIP configs, etc.) use SQL NULL. Go model uses `*T` for these.

4. **Transaction isolation**: Migration runner uses a single transaction per migration. DHCP allocation uses SERIALIZABLE or explicit row locking. Stats updates are asynchronous (fire-and-forget).

5. **Prepared statements**: All queries in Go code use parameterized queries (`$1`, `$2`, etc.) to prevent SQL injection.

6. **Connection pooling**: The Go app opens a single `*sql.DB` connection pool; concurrent requests share connections. Pool size tuned based on cluster size and request rate.

---

End of data-model.md
