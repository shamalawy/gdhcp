# gdhcp Implementation Contracts

**Status**: Binding specification for parallel implementation  
**Version**: 1.0  
**Date**: 2026-08-29

This document specifies exact Go interfaces, JSON schemas, and API contracts that all implementation units MUST conform to. This enables parallel implementation without breaking changes.

---

## Part 1: Core Go Interfaces

All interfaces live in `internal/{package}/interface.go` unless noted otherwise.

### Store Interfaces (internal/store/interface.go)

```go
package store

import (
  "context"
  "net"
  "time"
  "github.com/google/uuid"
)

// LeaseStore handles DHCP lease allocation, renewal, and expiry.
// All methods must be thread-safe and support concurrent calls.
type LeaseStore interface {
  // AllocateLease reserves a free IP in the pool for this client.
  // Returns ErrNoLeases if no IPs available after 3 retries.
  // Transaction: SERIALIZABLE isolation, SELECT...FOR UPDATE with retry loop.
  AllocateLease(ctx context.Context, poolID string, clientMAC string, clientID string) (*Lease, error)

  // RenewLease extends the lease expiry time for an existing lease.
  // Returns ErrLeaseNotFound if lease expired or MAC doesn't match.
  RenewLease(ctx context.Context, leaseID string, clientMAC string, newExpiry time.Time) (*Lease, error)

  // ReleaseLease marks a lease as released by the client.
  // Safe to call multiple times (idempotent).
  ReleaseLease(ctx context.Context, leaseID string, clientMAC string) error

  // GetLeaseByIP returns the lease for an IP if it exists and is not expired.
  // Returns nil, nil if no lease found (not an error).
  GetLeaseByIP(ctx context.Context, poolID string, ip net.IP) (*Lease, error)

  // GetLeaseByClient returns the active lease for a MAC+Client-ID, if any.
  // Returns nil, nil if not found.
  GetLeaseByClient(ctx context.Context, clientMAC string, clientID string) (*Lease, error)

  // GetLeasesByPool lists leases in a pool, optionally filtered by state.
  // Supports pagination: limit=100, offset=0 (default).
  GetLeasesByPool(ctx context.Context, poolID string, state string, offset, limit int) ([]*Lease, int64, error)

  // ExpireLeasesOlderThan marks all leases with expiry < cutoff as expired.
  // Called by background reaper; returns count of expired leases.
  ExpireLeasesOlderThan(ctx context.Context, cutoff time.Time) (int64, error)

  // ReservationForLease checks if a reservation exists for this binding key in pool.
  // Returns nil, nil if no reservation found.
  ReservationForLease(ctx context.Context, poolID string, bindingKey string) (*Reservation, error)
}

// ConfigStore fetches DHCP configuration (scopes, pools, options, templates).
// All methods must be thread-safe and support concurrent reads.
type ConfigStore interface {
  // GetVRFByInterface returns the VRF that owns this interface.
  // Returns default VRF if not found.
  GetVRFByInterface(ctx context.Context, ifname string) (*VRF, error)

  // GetVRFByID fetches a VRF by UUID.
  GetVRFByID(ctx context.Context, vrfID string) (*VRF, error)

  // GetScopesByVRF lists all scopes in a VRF.
  GetScopesByVRF(ctx context.Context, vrfID string) ([]*Scope, error)

  // GetScopeByID fetches a scope by UUID.
  GetScopeByID(ctx context.Context, scopeID string) (*Scope, error)

  // FindScopeByGiaddr returns the scope containing this giaddr (relay agent IP).
  // Used for relay agent Option 82 routing.
  FindScopeByGiaddr(ctx context.Context, giaddr net.IP) (*Scope, error)

  // FindScopeByNetwork returns a scope matching the given network CIDR.
  FindScopeByNetwork(ctx context.Context, vrfID string, network *net.IPNet) (*Scope, error)

  // GetPoolsByScope lists all pools in a scope, ordered by priority.
  GetPoolsByScope(ctx context.Context, scopeID string) ([]*Pool, error)

  // GetPoolByID fetches a pool by UUID.
  GetPoolByID(ctx context.Context, poolID string) (*Pool, error)

  // GetOptionsByScope fetches option values defined on a scope.
  GetOptionsByScope(ctx context.Context, scopeID string) ([]*DHCPOption, error)

  // GetOptionsByPool fetches option values defined on a pool.
  GetOptionsByPool(ctx context.Context, poolID string) ([]*DHCPOption, error)

  // GetOptionsByReservation fetches option values defined on a reservation.
  GetOptionsByReservation(ctx context.Context, reservationID string) ([]*DHCPOption, error)

  // GetGlobalOptions fetches globally-defined DHCP options.
  GetGlobalOptions(ctx context.Context) ([]*DHCPOption, error)

  // ResolveOption returns the effective option value traversing inheritance hierarchy.
  // Precedence: reservation > pool > scope > template > global.
  // Returns nil if option not found.
  ResolveOption(ctx context.Context, optCode int16, scopeID, poolID, reservationID string) (*DHCPOption, error)

  // GetTemplateByID fetches a template by UUID.
  GetTemplateByID(ctx context.Context, templateID string) (*Template, error)

  // GetTemplatesByScope lists templates applied to a scope (ordered by priority).
  GetTemplatesByScope(ctx context.Context, scopeID string) ([]*Template, error)

  // CanAllocateInPool checks if a specific IP can be allocated in pool.
  // Returns false if reserved, declined, or outside pool range.
  CanAllocateInPool(ctx context.Context, poolID string, ip net.IP) (bool, error)
}

// IPAMStore provides hierarchical IPAM prefix queries.
type IPAMStore interface {
  // GetPrefixByID fetches a prefix by UUID.
  GetPrefixByID(ctx context.Context, prefixID string) (*IPAMPrefix, error)

  // GetPrefixesByVRF lists all top-level prefixes in a VRF.
  GetPrefixesByVRF(ctx context.Context, vrfID string) ([]*IPAMPrefix, error)

  // GetChildPrefixes lists direct children of a prefix (next level in hierarchy).
  GetChildPrefixes(ctx context.Context, parentID string) ([]*IPAMPrefix, error)

  // GetAncestorPrefixes returns ancestors of a prefix (parent, grandparent, etc.).
  GetAncestorPrefixes(ctx context.Context, prefixID string) ([]*IPAMPrefix, error)

  // FindPrefixContaining returns immediate parent prefix containing this CIDR.
  FindPrefixContaining(ctx context.Context, vrfID string, cidr *net.IPNet) (*IPAMPrefix, error)

  // ListPrefixesInCIDR returns all prefixes (at any level) within a given CIDR.
  ListPrefixesInCIDR(ctx context.Context, vrfID string, cidr *net.IPNet) ([]*IPAMPrefix, error)
}

// StatsRecorder records DHCP and utilization statistics.
type StatsRecorder interface {
  // RecordDiscover increments DISCOVER counter for this scope.
  RecordDiscover(scopeID string)

  // RecordOffer increments OFFER counter for this scope.
  RecordOffer(scopeID string)

  // RecordRequest increments REQUEST counter for this scope.
  RecordRequest(scopeID string)

  // RecordAck increments ACK counter for this scope.
  RecordAck(scopeID string)

  // RecordNak increments NAK counter for this scope.
  RecordNak(scopeID string)

  // RecordDecline increments DECLINE counter for this scope.
  RecordDecline(scopeID string)

  // RecordRelease increments RELEASE counter for this scope.
  RecordRelease(scopeID string)

  // RecordNoLeases increments counter when pool exhausted.
  RecordNoLeases(scopeID string)

  // GetStats returns aggregated stats for a scope over a time window.
  GetStats(ctx context.Context, scopeID string, window time.Duration) (*DHCPStats, error)

  // SampleUtilization captures current utilization snapshot (called every 60 seconds).
  SampleUtilization(ctx context.Context) error
}

// UserStore manages users and API tokens.
type UserStore interface {
  // GetUserByID fetches user by ID.
  GetUserByID(ctx context.Context, userID int64) (*User, error)

  // GetUserByUsername fetches user by username.
  GetUserByUsername(ctx context.Context, username string) (*User, error)

  // CreateUser creates a new user (password already hashed).
  CreateUser(ctx context.Context, user *User) error

  // UpdateUser updates user fields (except password).
  UpdateUser(ctx context.Context, user *User) error

  // CreateAPIToken creates a new API token (token_hash should be bcrypt of raw token).
  CreateAPIToken(ctx context.Context, token *APIToken) error

  // ValidateAPIToken checks if token exists and is valid (not expired).
  ValidateAPIToken(ctx context.Context, tokenHash string) (*APIToken, error)

  // RevokeAPIToken deletes an API token by ID.
  RevokeAPIToken(ctx context.Context, tokenID int64) error

  // ListAPITokens returns all tokens for a user.
  ListAPITokens(ctx context.Context, userID int64) ([]*APIToken, error)
}

// ClusterStore manages cluster heartbeat and election state.
type ClusterStore interface {
  // GetClusterByID fetches cluster config.
  GetClusterByID(ctx context.Context, clusterID string) (*Cluster, error)

  // WriteHeartbeat updates this server's heartbeat (primary only).
  // Sets heartbeat_time = now(), role = 'PRIMARY'.
  // Returns error if row not found (server not registered).
  WriteHeartbeat(ctx context.Context, clusterID, serverID string) error

  // GetHeartbeats returns all heartbeats in a cluster.
  GetHeartbeats(ctx context.Context, clusterID string) ([]*Heartbeat, error)

  // ElectionQuery returns elected primary server ID per election algorithm.
  // Selects lowest node_id among servers with recent heartbeat.
  ElectionQuery(ctx context.Context, clusterID string) (string, error)

  // RegisterServer adds a server to cluster (self-registration on first boot).
  RegisterServer(ctx context.Context, server *ClusterServer) error

  // ListServers returns all servers in a cluster.
  ListServers(ctx context.Context, clusterID string) ([]*ClusterServer, error)

  // UpdateLeaseVersion increments config version (primary writes this on config change).
  UpdateLeaseVersion(ctx context.Context, clusterID, serverID string) (int, error)

  // UpdateLastConfigVersion updates config version applied by this server.
  UpdateLastConfigVersion(ctx context.Context, clusterID, serverID string, version int) error
}

// AuditStore logs all user actions for compliance.
type AuditStore interface {
  // LogAction records a user action.
  LogAction(ctx context.Context, log *AuditLog) error

  // ListAuditLog returns paginated audit logs.
  ListAuditLog(ctx context.Context, offset, limit int, filters map[string]interface{}) ([]*AuditLog, int64, error)
}
```

### Model Structs (internal/models/{entity}.go)

**Naming convention**: UpperCamelCase (PascalCase) in Go; lowercase_with_underscores in JSON/SQL.

```go
package models

import (
  "database/sql/driver"
  "encoding/json"
  "net"
  "time"
  "github.com/google/uuid"
)

// User represents a system user.
type User struct {
  ID            int64      `json:"id" db:"id"`
  Username      string     `json:"username" db:"username"`
  Email         string     `json:"email" db:"email"`
  PasswordHash  string     `json:"-" db:"password_hash"`  // Never exposed in API
  Description   *string    `json:"description" db:"description"`
  IsActive      bool       `json:"is_active" db:"is_active"`
  CreatedAt     time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
  CreatedByUser *User      `json:"created_by_user,omitempty" db:"-"`  // Nested; omit in list
  UpdatedByUser *User      `json:"updated_by_user,omitempty" db:"-"`
}

// APIToken represents a long-lived API access token.
type APIToken struct {
  ID            int64      `json:"id" db:"id"`
  UserID        int64      `json:"user_id" db:"user_id"`
  TokenHash     string     `json:"-" db:"token_hash"`  // Never exposed
  Description   *string    `json:"description" db:"description"`
  LastUsedAt    *time.Time `json:"last_used_at" db:"last_used_at"`
  ExpiresAt     *time.Time `json:"expires_at" db:"expires_at"`
  CreatedAt     time.Time  `json:"created_at" db:"created_at"`
}

// VRF represents a virtual routing forwarding domain.
type VRF struct {
  ID                   string     `json:"id" db:"id"`
  Name                 string     `json:"name" db:"name"`
  RouteDistinguisher   *string    `json:"route_distinguisher" db:"route_distinguisher"`
  Description          *string    `json:"description" db:"description"`
  IsDefault            bool       `json:"is_default" db:"is_default"`
  CreatedAt            time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt            time.Time  `json:"updated_at" db:"updated_at"`
}

// IPAMPrefix represents a prefix in the IPAM hierarchy.
type IPAMPrefix struct {
  ID                     string     `json:"id" db:"id"`
  VRFID                  string     `json:"vrf_id" db:"vrf_id"`
  Prefix                 *net.IPNet `json:"prefix" db:"prefix"`  // CIDR
  ParentID               *string    `json:"parent_id" db:"parent_id"`
  PrefixType             string     `json:"prefix_type" db:"prefix_type"`  // 'aggregate', 'container', 'subnet'
  Name                   *string    `json:"name" db:"name"`
  Description            *string    `json:"description" db:"description"`
  UtilizationPercentage  *int       `json:"utilization_percentage" db:"utilization_percentage"`
  CreatedAt              time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt              time.Time  `json:"updated_at" db:"updated_at"`
}

// Cluster represents an HA cluster.
type Cluster struct {
  ID                 string     `json:"id" db:"id"`
  Name               string     `json:"name" db:"name"`
  Description        *string    `json:"description" db:"description"`
  VIPAddress         *net.IP    `json:"vip_address" db:"vip_address"`
  VIPInterface       *string    `json:"vip_interface" db:"vip_interface"`
  VIPVirtualMAC      *net.HardwareAddr `json:"vip_virtual_mac" db:"vip_virtual_mac"`
  HeartbeatInterval  int        `json:"heartbeat_interval_ms" db:"heartbeat_interval_ms"`
  HeartbeatTTL       int        `json:"heartbeat_ttl_ms" db:"heartbeat_ttl_ms"`
  ElectionInterval   int        `json:"election_interval_ms" db:"election_interval_ms"`
  CreatedAt          time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
}

// DHCPScope represents a DHCP-served subnet.
type DHCPScope struct {
  ID             string     `json:"id" db:"id"`
  VRFID          string     `json:"vrf_id" db:"vrf_id"`
  ClusterID      string     `json:"cluster_id" db:"cluster_id"`
  IPAMPrefixID   string     `json:"ipam_prefix_id" db:"ipam_prefix_id"`
  Name           string     `json:"name" db:"name"`
  Description    *string    `json:"description" db:"description"`
  Network        *net.IPNet `json:"network" db:"network"`  // Cached CIDR
  IsEnabled      bool       `json:"is_enabled" db:"is_enabled"`
  CreatedAt      time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// DHCPPool represents an IP address range within a scope.
type DHCPPool struct {
  ID             string     `json:"id" db:"id"`
  ScopeID        string     `json:"scope_id" db:"scope_id"`
  Name           string     `json:"name" db:"name"`
  Description    *string    `json:"description" db:"description"`
  RangeStart     net.IP     `json:"range_start" db:"range_start"`
  RangeEnd       net.IP     `json:"range_end" db:"range_end"`
  IsEnabled      bool       `json:"is_enabled" db:"is_enabled"`
  Priority       int        `json:"priority" db:"priority"`
  CreatedAt      time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// DHCPReservation represents a fixed IP reservation.
type DHCPReservation struct {
  ID            string     `json:"id" db:"id"`
  PoolID        string     `json:"pool_id" db:"pool_id"`
  Hostname      *string    `json:"hostname" db:"hostname"`
  Description   *string    `json:"description" db:"description"`
  ReservedIP    net.IP     `json:"reserved_ip" db:"reserved_ip"`
  BindingKey    string     `json:"binding_key" db:"binding_key"`  // 'mac:...' or 'duid:...'
  BindingType   string     `json:"binding_type" db:"binding_type"`  // 'mac', 'client_id', 'hostname'
  IsEnabled     bool       `json:"is_enabled" db:"is_enabled"`
  CreatedAt     time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

// DHCPLease represents an active DHCP lease.
type DHCPLease struct {
  ID              string     `json:"id" db:"id"`
  PoolID          string     `json:"pool_id" db:"pool_id"`
  IPAddress       net.IP     `json:"ip_address" db:"ip_address"`
  ClientMAC       *net.HardwareAddr `json:"client_mac" db:"client_mac"`
  ClientID        []byte     `json:"client_id,omitempty" db:"client_id"`
  ClientHostname  *string    `json:"client_hostname" db:"client_hostname"`
  BindingState    string     `json:"binding_state" db:"binding_state"`  // 'active', 'expired', 'released', 'abandoned'
  BindingServerID string     `json:"binding_server_id" db:"binding_server_id"`
  LeaseStart      time.Time  `json:"lease_start" db:"lease_start"`
  LeaseEnd        time.Time  `json:"lease_end" db:"lease_end"`
  RenewalTime     *time.Time `json:"renewal_time" db:"renewal_time"`
  RebindingTime   *time.Time `json:"rebinding_time" db:"rebinding_time"`
  CreatedAt       time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
}

// DHCPOption represents an option value at a scope level.
type DHCPOption struct {
  ID            string     `json:"id" db:"id"`
  OptionCode    int16      `json:"option_code" db:"option_code"`
  ScopeLevel    string     `json:"scope_level" db:"scope_level"`  // 'global', 'template', 'scope', 'pool', 'reservation'
  ScopeEntityID string     `json:"scope_entity_id" db:"scope_entity_id"`
  Value         string     `json:"value" db:"value"`
  IsEnabled     bool       `json:"is_enabled" db:"is_enabled"`
  CreatedAt     time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

// Heartbeat represents a cluster server heartbeat entry.
type Heartbeat struct {
  ID                 string     `json:"id" db:"id"`
  ClusterID          string     `json:"cluster_id" db:"cluster_id"`
  ServerID           string     `json:"server_id" db:"server_id"`
  HeartbeatTime      time.Time  `json:"heartbeat_time" db:"heartbeat_time"`
  Role               string     `json:"role" db:"role"`  // 'PRIMARY' or 'SECONDARY'
  VIPAssigned        bool       `json:"vip_assigned" db:"vip_assigned"`
  LeaseVersion       int        `json:"lease_version" db:"lease_version"`
  LastConfigVersion  int        `json:"last_config_version" db:"last_config_version"`
  CreatedAt          time.Time  `json:"created_at" db:"created_at"`
  UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
}

// DHCPStats represents aggregated statistics for a scope.
type DHCPStats struct {
  ScopeID        string     `json:"scope_id"`
  DiscoverCount  int64      `json:"discover_count"`
  OfferCount     int64      `json:"offer_count"`
  RequestCount   int64      `json:"request_count"`
  AckCount       int64      `json:"ack_count"`
  NakCount       int64      `json:"nak_count"`
  DeclineCount   int64      `json:"decline_count"`
  ReleaseCount   int64      `json:"release_count"`
  NoLeasesCount  int64      `json:"no_leases_count"`
}
```

---

## Part 2: REST API Contract

### Base URL: `/api/v1`

### Authentication Header (All Requests)

```
Authorization: Bearer <jwt_token or api_token>
```

### Response Envelope (Success)

**2xx responses** return the resource or list directly:

```json
{
  "id": "uuid",
  "name": "example",
  "created_at": "2026-08-29T10:30:00Z"
}
```

**List responses** include pagination:

```json
{
  "items": [...],
  "total": 1234,
  "limit": 100,
  "offset": 0
}
```

### Error Envelope (All Errors)

**HTTP status**: 400, 401, 403, 404, 409, 500, 503

```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable message",
    "details": {
      "field": "fieldname",
      "issue": "issue_type"
    },
    "trace_id": "req_<uuid>"
  }
}
```

### Common Query Parameters

```
GET /api/v1/{entity}
  ?limit=100        # Default: 100, Max: 1000
  &offset=0         # Default: 0 (pagination)
  &cursor=<token>   # Keyset pagination (alternative to offset)
  &sort=-created_at,name  # Sort fields (prefix - for DESC)
  &filter={"field":"value"}  # JSON-encoded filter
  &search=query     # Global substring search
```

### Endpoints Summary

**All endpoints must support**:
- `GET /api/v1/{entity}` — List (paginated, filterable)
- `GET /api/v1/{entity}/{id}` — Read single
- `POST /api/v1/{entity}` — Create (requires body JSON)
- `PUT /api/v1/{entity}/{id}` — Update full (requires body)
- `PATCH /api/v1/{entity}/{id}` — Update partial (requires body)
- `DELETE /api/v1/{entity}/{id}` — Delete

**Special endpoints**:
- `GET /api/v1/auth/login` — POST instead (POST /api/v1/auth/login)
- `POST /api/v1/auth/logout` — Revoke session token
- `POST /api/v1/auth/refresh` — Get new JWT (from refresh token)
- `GET /api/v1/search` — Global search
- `GET /api/v1/ipam/tree` — IPAM hierarchy tree
- `GET /api/v1/cluster/health` — HA cluster status
- `GET /api/v1/dashboard/stats` — Dashboard statistics
- `POST /api/v1/{entity}/{id}/action` — Custom actions (failover, bootstrap, etc.)

### JSON Casing Convention

**Request/Response**: camelCase for JSON, but...
**Database/Go**: SQL uses snake_case, Go structs use PascalCase

**Example mapping**:
```json
{
  "id": "uuid",
  "vrfId": "uuid-2",
  "createdAt": "2026-08-29T10:30:00Z",
  "isEnabled": true,
  "clientMac": "aa:bb:cc:dd:ee:ff"
}
```

Maps to Go struct:
```go
type Example struct {
  ID string `json:"id"`
  VRFID string `json:"vrfId"`  // Marshals as vrfId
  CreatedAt time.Time `json:"createdAt"`
  IsEnabled bool `json:"isEnabled"`
  ClientMAC *net.HardwareAddr `json:"clientMac"`
}
```

---

## Part 3: DHCP Protocol Contract

### Packet Structure (insomniacslk/dhcp)

```go
// Received DHCP packet wrapper
type DHCPPacket struct {
  ReceivedAt   time.Time
  Interface    string
  SourceAddr   *net.UDPAddr
  MessageType  dhcpv4.MessageType  // 1=DISCOVER, 2=OFFER, 3=REQUEST, etc.
  Packet       *dhcpv4.DHCPv4      // Parsed packet
  XSID         [4]byte             // Transaction ID
  RelayedFromVRF string             // Non-empty if Option 82
}
```

### Option Codes (IANA Standard)

Codes 1–254 seeded in dhcp_option_catalog table. Implementation must:
1. Encode/decode all option types via insomniacslk/dhcp library
2. Support option inheritance (reservation > pool > scope > template > global)
3. Lazy-load only options in client's Parameter Request List (PRL, option 55)

### Lease Time (RFC 2131 Timing)

```
T1 (renewal time) = 50% of lease time (40% in HA mode)
T2 (rebinding time) = 87.5% of lease time
Lease End = lease_time (seconds from allocation)

Example: lease_time = 86400 seconds (24 hours)
  T1 = 43200 (12 hours)
  T2 = 75600 (21 hours)
  Lease End = 86400 (24 hours)
```

---

## Part 4: Configuration File Schema (config.yaml)

```yaml
---
server:
  api_addr: "0.0.0.0:8080"
  api_tls_enabled: false

database:
  dsn: "postgres://user:pass@localhost:5432/gdhcp"
  max_open_conns: 25
  max_idle_conns: 5
  lease_retention_days: 90
  option_cache_ttl: "60s"

dhcp:
  interfaces:
    - name: "eth0"
      vrf_id: "default"

ha:
  enabled: true
  cluster_id: "prod-cluster"
  server_id: "dhcp-01"
  node_id: 1
  vip_address: "10.0.0.10/24"
  vip_interface: "eth0"
  heartbeat_interval_ms: 1000
  heartbeat_ttl_ms: 3000
  election_interval_ms: 2000

logging:
  level: "info"
  format: "json"

metrics:
  enabled: true
  listen_addr: "0.0.0.0:9090"
```

---

## Part 5: Database Migration Contract

Each migration file (e.g., `0001_initial_schema.sql`) must:

1. Be wrapped in `BEGIN; ... COMMIT;` (single transaction)
2. Include all necessary `CREATE TABLE`, `CREATE INDEX`, `ALTER TABLE` statements
3. Use PostgreSQL 13+ features (EXCLUSION constraints, advisory locks recommended)
4. Be idempotent where possible (use `IF NOT EXISTS`, `ON CONFLICT DO NOTHING`)
5. Have a header comment: `-- Migration 0001: description`

Migration runner (migrate.go) must:
1. Acquire advisory lock before running migrations
2. Execute each migration in transaction order
3. Track executed migrations in `schema_migrations` table
4. Skip already-executed migrations (idempotent)

---

## Part 6: Error Codes (Canonical)

| Code | HTTP | Meaning | Example |
|------|------|---------|---------|
| `INVALID_INPUT` | 400 | Bad request body or query params | Missing required field |
| `UNAUTHORIZED` | 401 | Missing or invalid auth token | Expired JWT |
| `FORBIDDEN` | 403 | Insufficient permissions for action | Non-admin tries to create user |
| `NOT_FOUND` | 404 | Resource does not exist | Scope ID not found |
| `CONFLICT` | 409 | Resource already exists or constraint violated | Duplicate VRF name, overlapping CIDR |
| `INTERNAL_ERROR` | 500 | Server error (panic, DB failure, etc.) | Database connection lost |
| `SERVICE_UNAVAILABLE` | 503 | Database or dependency unreachable | Postgres down |

---

## Part 7: Concurrency & Locking

### DHCP Lease Allocation

**Isolation Level**: `SERIALIZABLE`  
**Locking**: `SELECT ... FOR UPDATE SKIP LOCKED`  
**Retry Loop**: Up to 3 attempts with exponential backoff (10–50ms)

```go
// Pseudocode
for attempt := 0; attempt < 3; attempt++ {
  txn := BeginTx(SERIALIZABLE)
  defer txn.Rollback()

  // Fetch free IP with lock
  freeIP := txn.QueryRow("SELECT ip FROM leases WHERE pool_id = $1 AND state = 'free' FOR UPDATE SKIP LOCKED LIMIT 1")

  // Atomically allocate
  result := txn.Exec("UPDATE leases SET state = 'active', mac = $1 ... WHERE ip = $2 AND state = 'free'")

  if result.RowsAffected == 0 {
    // Another server won; retry next IP
    continue
  }

  txn.Commit()
  return lease
}
return error("allocation failed")
```

### Config Updates

**Atomicity**: Entire config swapped via mutex + pointer update  
**Cache Invalidation**: On write, invalidate affected (scope_id, pool_id) entries  
**Propagation**: LISTEN/NOTIFY with fallback polling (5 seconds)

---

## Part 8: Metrics (Prometheus)

All metrics registered in `internal/metrics/registry.go`.

**Naming convention**: `gdhcp_{subsystem}_{metric_name}_{unit}`

**Vectors**: Include labels for entity IDs, scopes, servers.

**Example metrics**:

```
gdhcp_dhcp_discover_total{scope_id="uuid"} 1000
gdhcp_dhcp_offer_total{scope_id="uuid"} 999
gdhcp_dhcp_allocation_retries_bucket{scope_id="uuid",le="1"} 800
gdhcp_dhcp_allocation_retries_bucket{scope_id="uuid",le="2"} 950
gdhcp_dhcp_allocation_retries_bucket{scope_id="uuid",le="3"} 1000

gdhcp_heartbeat_write_duration_ms_bucket{server_id="uuid",le="50"} 900
gdhcp_election_query_duration_ms_bucket{cluster_id="uuid",le="100"} 1000

gdhcp_lease_allocation_conflicts_total{pool_id="uuid"} 12
gdhcp_vip_assignment_failures_total{cluster_id="uuid"} 0
gdhcp_db_partition_detected_total{cluster_id="uuid"} 1
```

---

End of CONTRACTS.md
