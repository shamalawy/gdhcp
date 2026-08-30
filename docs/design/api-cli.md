# gdhcp REST API and CLI Design

## Overview

This document specifies the complete REST API and command-line interface (CLI) for gdhcp, a high-availability DHCP server with integrated IPAM. The API uses JSON over HTTPS at `/api/v1`, and the CLI is a single binary (`gdhcpd`) with subcommands.

**Key Design Principles:**
- All entities support full CRUD operations
- Pagination, filtering, and sorting on all list endpoints
- Consistent error envelope across all responses
- Authentication via JWT (GUI sessions) and static API tokens (CLI/automation)
- Role-based access control (admin, operator, viewer)
- Server-Sent Events (SSE) for live dashboard updates (chosen over WebSocket for REST alignment and operational simplicity)
- CLI mirrors API structure and is co-located in a single binary with serve/migrate subcommands

---

## 1. Authentication & Authorization

### 1.1 Token Types

**Session JWT (GUI)**
- Issued by `/api/v1/auth/login` on username/password auth
- TTL: 24 hours (configurable)
- Refresh token endpoint: `/api/v1/auth/refresh`
- Revocation on logout: `/api/v1/auth/logout`
- Payload includes: `user_id`, `username`, `roles`, `iat`, `exp`

**API Tokens (CLI/Automation)**
- Static, long-lived tokens (no expiry by default)
- Created via `/api/v1/users/{user_id}/tokens`
- Format: `sk_<random_32_hex_chars>` (prefix for identification)
- Revocable: DELETE `/api/v1/users/{user_id}/tokens/{token_id}`
- One token per use case; users can have multiple tokens
- Stored as bcrypt hashes in the database

### 1.2 RBAC Roles

| Role | Permissions |
|------|---|
| `admin` | All CRUD + cluster/user management + token creation/revocation |
| `operator` | All CRUD except cluster/user management |
| `viewer` | Read-only access to all resources |

### 1.3 Header-Based Authentication

All API requests require one of:
- `Authorization: Bearer <jwt_token>` (GUI sessions)
- `Authorization: Bearer <api_token>` (CLI/automation)

Unauthenticated requests return `401 Unauthorized`.

---

## 2. Error Handling

All errors use a consistent envelope:

```json
{
  "error": {
    "code": "INVALID_INPUT",
    "message": "Friendly description of the error",
    "details": {
      "field": "scope_id",
      "issue": "not_found"
    },
    "trace_id": "req_abc123def456"
  }
}
```

### Common Error Codes

| Code | HTTP Status | Meaning |
|------|---|---|
| `INVALID_INPUT` | 400 | Request body or query params invalid |
| `UNAUTHORIZED` | 401 | Missing or invalid auth token |
| `FORBIDDEN` | 403 | Insufficient permissions |
| `NOT_FOUND` | 404 | Resource does not exist |
| `CONFLICT` | 409 | Resource already exists or constraint violated (e.g., IP overlap) |
| `INTERNAL_ERROR` | 500 | Server error |
| `SERVICE_UNAVAILABLE` | 503 | Database or dependency unreachable |

---

## 3. Common Query Parameters

### Pagination

All list endpoints support:
- `limit=<int>` (default: 100, max: 1000)
- `offset=<int>` (default: 0)

Response includes:
```json
{
  "items": [...],
  "total": 1234,
  "limit": 100,
  "offset": 0
}
```

### Filtering

Filters applied via `filter` query parameter (URL-encoded JSON):
```
GET /api/v1/scopes?filter={"vrf_id":"vrf_prod","status":"active"}
```

Supported on all entity list endpoints. Field names match entity JSON keys.

### Sorting

Sorting via `sort` query parameter (comma-separated, prefix with `-` for descending):
```
GET /api/v1/leases?sort=-created_at,hostname
```

Default sort per entity documented in endpoint table.

### Search

Global search via `search` parameter on entity list endpoints:
```
GET /api/v1/scopes?search=prod-web
```

Searches name, description, tags, labels. Case-insensitive substring match.

---

## 4. Core Data Structures

### 4.1 VRF (Virtual Routing Forwarding)

```json
{
  "id": "vrf_aAbBcCdDeEfF123456",
  "name": "production",
  "description": "Production network segment",
  "route_distinguisher": "65000:1",
  "is_default": false,
  "tags": ["production", "critical"],
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

### 4.2 Prefix (IP Space - top-level aggregates)

```json
{
  "id": "prefix_aAbBcCdDeEfF123456",
  "vrf_id": "vrf_aAbBcCdDeEfF123456",
  "cidr": "10.0.0.0/8",
  "name": "Core Network",
  "description": "All internal network space",
  "tags": ["internal"],
  "custom_fields": {"region": "us-east"},
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

### 4.3 Subnet (Child IPAM objects; may or may not have a DHCP Scope)

```json
{
  "id": "subnet_aAbBcCdDeEfF123456",
  "prefix_id": "prefix_aAbBcCdDeEfF123456",
  "vrf_id": "vrf_aAbBcCdDeEfF123456",
  "cidr": "10.1.0.0/24",
  "name": "Web Tier - Zone A",
  "description": "Web servers in zone A",
  "tags": ["web", "zone-a"],
  "custom_fields": {"department": "platform"},
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

### 4.4 Scope (DHCP Scope; 1:1 relationship with a Subnet)

```json
{
  "id": "scope_aAbBcCdDeEfF123456",
  "subnet_id": "subnet_aAbBcCdDeEfF123456",
  "vrf_id": "vrf_aAbBcCdDeEfF123456",
  "name": "web-tier-zone-a",
  "description": "DHCP for web servers in zone A",
  "enabled": true,
  "lease_time": 86400,
  "renewal_time": 43200,
  "rebind_time": 75600,
  "tags": ["web", "zone-a"],
  "template_id": "template_aAbBcCdDeEfF123456",
  "option_values": {...},
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

### 4.5 Pool (IP Pool within a Scope)

```json
{
  "id": "pool_aAbBcCdDeEfF123456",
  "scope_id": "scope_aAbBcCdDeEfF123456",
  "name": "dynamic-pool-1",
  "description": "Dynamic addresses for web tier",
  "start_ip": "10.1.0.10",
  "end_ip": "10.1.0.200",
  "enabled": true,
  "tags": ["dynamic"],
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

### 4.6 Reservation (Fixed IP mapping within a Pool)

```json
{
  "id": "reservation_aAbBcCdDeEfF123456",
  "pool_id": "pool_aAbBcCdDeEfF123456",
  "scope_id": "scope_aAbBcCdDeEfF123456",
  "mac_address": "00:11:22:33:44:55",
  "ip_address": "10.1.0.50",
  "hostname": "web-server-01",
  "description": "Production web server",
  "enabled": true,
  "tags": ["production"],
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

### 4.7 Lease (Active DHCP Lease)

```json
{
  "id": "lease_aAbBcCdDeEfF123456",
  "pool_id": "pool_aAbBcCdDeEfF123456",
  "scope_id": "scope_aAbBcCdDeEfF123456",
  "mac_address": "aa:bb:cc:dd:ee:ff",
  "ip_address": "10.1.0.101",
  "hostname": "client-laptop",
  "state": "active",
  "expires_at": "2026-01-03T12:00:00Z",
  "created_at": "2026-01-01T12:00:00Z",
  "last_activity": "2026-01-02T11:55:00Z"
}
```

### 4.8 Template (Reusable DHCP Settings)

```json
{
  "id": "template_aAbBcCdDeEfF123456",
  "name": "standard-web",
  "description": "Standard DHCP settings for web tier",
  "scope_template": {
    "lease_time": 86400,
    "renewal_time": 43200,
    "rebind_time": 75600,
    "option_values": {
      "6": ["8.8.8.8", "8.8.4.4"],
      "15": "example.com",
      "3": ["10.1.0.1"]
    }
  },
  "tags": ["web"],
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

### 4.9 Option (DHCP Option Definition with Catalog Info)

```json
{
  "id": "option_aAbBcCdDeEfF123456",
  "code": 6,
  "name": "Domain Name Servers",
  "description": "IANA DHCPv4 Option 6. Specifies a list of DNS servers available to the client. Clients use these servers to perform DNS lookups. Format: comma-separated IP addresses.",
  "data_type": "ip_list",
  "is_standard": true,
  "custom": false,
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

Supported `data_type` values: `string`, `int`, `ip`, `ip_list`, `mac_address`, `boolean`, `binary`, `domain_name_list`, `time_offset`.

### 4.10 OptionValue (Option assignment to a Scope or Pool)

```json
{
  "id": "option_value_aAbBcCdDeEfF123456",
  "scope_id": "scope_aAbBcCdDeEfF123456",
  "pool_id": null,
  "option_code": 6,
  "value": ["8.8.8.8", "8.8.4.4"],
  "description": "Public DNS servers",
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

Note: `pool_id` is optional; if set, option applies only to that pool within the scope.

### 4.11 Cluster (HA Cluster)

```json
{
  "id": "cluster_aAbBcCdDeEfF123456",
  "name": "primary-cluster",
  "description": "Primary HA cluster",
  "virtual_ip": "10.0.0.1",
  "enabled": true,
  "tags": ["production"],
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

### 4.12 Server (DHCP Server Node in a Cluster)

```json
{
  "id": "server_aAbBcCdDeEfF123456",
  "cluster_id": "cluster_aAbBcCdDeEfF123456",
  "hostname": "dhcp-01.example.com",
  "ip_address": "10.0.0.11",
  "status": "healthy",
  "is_primary": true,
  "port": 8080,
  "tags": ["production"],
  "last_heartbeat": "2026-01-02T11:59:00Z",
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z"
}
```

### 4.13 User (Administrative User)

```json
{
  "id": "user_aAbBcCdDeEfF123456",
  "username": "alice",
  "email": "alice@example.com",
  "roles": ["operator"],
  "enabled": true,
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-02T12:00:00Z",
  "last_login": "2026-01-02T11:00:00Z"
}
```

### 4.14 APIToken (Personal Access Token)

```json
{
  "id": "token_aAbBcCdDeEfF123456",
  "user_id": "user_aAbBcCdDeEfF123456",
  "name": "ci-automation",
  "token": "sk_abc123def456ghi789jkl012mno345",
  "last_used_at": "2026-01-02T10:00:00Z",
  "created_at": "2026-01-01T12:00:00Z",
  "expires_at": null
}
```

Note: `token` field only returned on creation; subsequent GETs omit it.

### 4.15 Tag (Metadata Label)

```json
{
  "id": "tag_aAbBcCdDeEfF123456",
  "name": "production",
  "color": "#ff5722",
  "description": "Production resources",
  "usage_count": 42,
  "created_at": "2026-01-01T12:00:00Z"
}
```

---

## 5. REST API Endpoints

### 5.1 Authentication Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/v1/auth/login` | Issue session JWT (username/password) |
| POST | `/api/v1/auth/refresh` | Refresh expired JWT |
| POST | `/api/v1/auth/logout` | Revoke session JWT |
| GET | `/api/v1/auth/me` | Get authenticated user info |

**POST /api/v1/auth/login**
```json
Request:
{
  "username": "alice",
  "password": "secure_password"
}

Response (200):
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "expires_in": 86400,
  "user": {
    "id": "user_aAbBcCdDeEfF123456",
    "username": "alice",
    "roles": ["operator"]
  }
}
```

### 5.2 VRF Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/vrfs` | List VRFs (paginated) |
| POST | `/api/v1/vrfs` | Create VRF |
| GET | `/api/v1/vrfs/{vrf_id}` | Get VRF details |
| PUT | `/api/v1/vrfs/{vrf_id}` | Update VRF |
| DELETE | `/api/v1/vrfs/{vrf_id}` | Delete VRF |

### 5.3 Prefix (IP Space) Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/prefixes` | List prefixes (paginated) |
| POST | `/api/v1/prefixes` | Create prefix |
| GET | `/api/v1/prefixes/{prefix_id}` | Get prefix details |
| PUT | `/api/v1/prefixes/{prefix_id}` | Update prefix |
| DELETE | `/api/v1/prefixes/{prefix_id}` | Delete prefix |
| GET | `/api/v1/prefixes/{prefix_id}/children` | Get child subnets with utilization |

### 5.4 Subnet Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/subnets` | List subnets (paginated) |
| POST | `/api/v1/subnets` | Create subnet |
| GET | `/api/v1/subnets/{subnet_id}` | Get subnet details |
| PUT | `/api/v1/subnets/{subnet_id}` | Update subnet |
| DELETE | `/api/v1/subnets/{subnet_id}` | Delete subnet |

### 5.5 Scope (DHCP Scope) Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/scopes` | List scopes (paginated) |
| POST | `/api/v1/scopes` | Create scope |
| GET | `/api/v1/scopes/{scope_id}` | Get scope details |
| PUT | `/api/v1/scopes/{scope_id}` | Update scope |
| DELETE | `/api/v1/scopes/{scope_id}` | Delete scope |
| GET | `/api/v1/scopes/{scope_id}/pools` | List pools in scope |
| GET | `/api/v1/scopes/{scope_id}/stats` | Get scope utilization stats |

### 5.6 Pool Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/pools` | List pools (paginated) |
| POST | `/api/v1/pools` | Create pool |
| GET | `/api/v1/pools/{pool_id}` | Get pool details |
| PUT | `/api/v1/pools/{pool_id}` | Update pool |
| DELETE | `/api/v1/pools/{pool_id}` | Delete pool |
| GET | `/api/v1/pools/{pool_id}/stats` | Get pool utilization stats |
| GET | `/api/v1/pools/{pool_id}/reservations` | List reservations in pool (paginated) |
| POST | `/api/v1/pools/{pool_id}/reservations` | Add reservation to pool |

### 5.7 Reservation Endpoints (under Pools per Product Requirement)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/pools/{pool_id}/reservations` | List pool reservations |
| POST | `/api/v1/pools/{pool_id}/reservations` | Create reservation |
| GET | `/api/v1/pools/{pool_id}/reservations/{reservation_id}` | Get reservation |
| PUT | `/api/v1/pools/{pool_id}/reservations/{reservation_id}` | Update reservation |
| DELETE | `/api/v1/pools/{pool_id}/reservations/{reservation_id}` | Delete reservation |

### 5.8 Lease Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/leases` | List leases (paginated) |
| GET | `/api/v1/leases/{lease_id}` | Get lease details |
| POST | `/api/v1/leases/{lease_id}/release` | Release lease (action) |
| POST | `/api/v1/leases/{lease_id}/wake` | Send WoL packet to lease (action) |

### 5.9 Template Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/templates` | List templates (paginated) |
| POST | `/api/v1/templates` | Create template |
| GET | `/api/v1/templates/{template_id}` | Get template details |
| PUT | `/api/v1/templates/{template_id}` | Update template |
| DELETE | `/api/v1/templates/{template_id}` | Delete template |

### 5.10 Option Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/options/catalog` | List all IANA DHCPv4 options with descriptions |
| GET | `/api/v1/options` | List custom options (paginated) |
| POST | `/api/v1/options` | Create custom option |
| GET | `/api/v1/options/{option_id}` | Get option details |
| PUT | `/api/v1/options/{option_id}` | Update option |
| DELETE | `/api/v1/options/{option_id}` | Delete option |
| GET | `/api/v1/scopes/{scope_id}/option-values` | Get option values for scope |
| PUT | `/api/v1/scopes/{scope_id}/option-values` | Bulk set option values for scope |
| GET | `/api/v1/pools/{pool_id}/option-values` | Get option values for pool |
| PUT | `/api/v1/pools/{pool_id}/option-values` | Bulk set option values for pool |

**GET /api/v1/options/catalog** (Sample Response)
```json
{
  "items": [
    {
      "code": 1,
      "name": "Subnet Mask",
      "description": "IANA DHCPv4 Option 1. The subnet mask to be used on this network. Format: dotted-decimal IP address.",
      "data_type": "ip"
    },
    {
      "code": 3,
      "name": "Router",
      "description": "IANA DHCPv4 Option 3. The IP address of routers on the client's subnet. Format: comma-separated IP addresses.",
      "data_type": "ip_list"
    },
    {
      "code": 6,
      "name": "Domain Name Servers",
      "description": "IANA DHCPv4 Option 6. Specifies a list of DNS servers. Format: comma-separated IP addresses.",
      "data_type": "ip_list"
    }
  ],
  "total": 61
}
```

### 5.11 Cluster Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/clusters` | List clusters (paginated) |
| POST | `/api/v1/clusters` | Create cluster |
| GET | `/api/v1/clusters/{cluster_id}` | Get cluster details |
| PUT | `/api/v1/clusters/{cluster_id}` | Update cluster |
| DELETE | `/api/v1/clusters/{cluster_id}` | Delete cluster |
| GET | `/api/v1/clusters/{cluster_id}/servers` | List servers in cluster |

### 5.12 Server Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/servers` | List servers (paginated) |
| POST | `/api/v1/servers` | Create server |
| GET | `/api/v1/servers/{server_id}` | Get server details |
| PUT | `/api/v1/servers/{server_id}` | Update server |
| DELETE | `/api/v1/servers/{server_id}` | Delete server |

### 5.13 User Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/users` | List users (admin only; paginated) |
| POST | `/api/v1/users` | Create user (admin only) |
| GET | `/api/v1/users/{user_id}` | Get user details |
| PUT | `/api/v1/users/{user_id}` | Update user (self or admin) |
| DELETE | `/api/v1/users/{user_id}` | Delete user (admin only) |
| GET | `/api/v1/users/{user_id}/tokens` | List user's API tokens |
| POST | `/api/v1/users/{user_id}/tokens` | Create API token |
| DELETE | `/api/v1/users/{user_id}/tokens/{token_id}` | Revoke API token |

### 5.14 Tag Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/tags` | List tags (paginated) |
| POST | `/api/v1/tags` | Create tag |
| GET | `/api/v1/tags/{tag_id}` | Get tag details |
| PUT | `/api/v1/tags/{tag_id}` | Update tag |
| DELETE | `/api/v1/tags/{tag_id}` | Delete tag |

### 5.15 IPAM Tree & Hierarchy Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/ipam/tree/{prefix_id}` | Get IPAM tree starting from prefix (with children and utilization) |
| GET | `/api/v1/ipam/tree` | Get full IPAM tree (all prefixes as roots) |

**GET /api/v1/ipam/tree/{prefix_id}** (Sample Response)
```json
{
  "id": "prefix_aAbBcCdDeEfF123456",
  "type": "prefix",
  "cidr": "10.0.0.0/8",
  "name": "Core Network",
  "utilization": {
    "total_ips": 16777216,
    "used_ips": 2048,
    "reserved_ips": 256,
    "utilization_percent": 13.8
  },
  "children": [
    {
      "id": "subnet_bBcCdDeEfF123456aA",
      "type": "subnet",
      "cidr": "10.1.0.0/24",
      "name": "Web Tier",
      "utilization": {
        "total_ips": 256,
        "used_ips": 180,
        "reserved_ips": 20,
        "utilization_percent": 78.1
      },
      "scope_id": "scope_cCdDeEfF123456aAbB",
      "children": [
        {
          "id": "pool_dDeEfF123456aAbBcC",
          "type": "pool",
          "name": "dynamic-pool-1",
          "start_ip": "10.1.0.10",
          "end_ip": "10.1.0.200",
          "utilization": {
            "total_ips": 191,
            "used_ips": 150,
            "reserved_ips": 10,
            "utilization_percent": 83.8
          }
        }
      ]
    }
  ]
}
```

### 5.16 Global Search Endpoint

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/search` | Global search across all resources |

**GET /api/v1/search?q=web-server&type=reservation,lease** (Sample Response)
```json
{
  "results": [
    {
      "type": "reservation",
      "id": "reservation_aAbBcCdDeEfF123456",
      "score": 0.95,
      "highlight": "web-server-01 (00:11:22:33:44:55)",
      "url": "/api/v1/pools/pool_xyz/reservations/reservation_aAbBcCdDeEfF123456"
    },
    {
      "type": "lease",
      "id": "lease_bBcCdDeEfF123456aA",
      "score": 0.87,
      "highlight": "web-server-02 (aa:bb:cc:dd:ee:ff)",
      "url": "/api/v1/leases/lease_bBcCdDeEfF123456aA"
    }
  ],
  "total": 2
}
```

Query parameters:
- `q` (required): search query
- `type` (optional, comma-separated): filter by type (vrf, prefix, subnet, scope, pool, reservation, lease, template, cluster, server, user, tag)
- `limit` (optional): max results (default 50)

### 5.17 Utilization & Stats Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/stats/global` | Global utilization across all scopes/pools |
| GET | `/api/v1/stats/cluster/{cluster_id}` | Cluster-level stats |
| GET | `/api/v1/stats/scope/{scope_id}` | Scope-level stats |
| GET | `/api/v1/stats/pool/{pool_id}` | Pool-level stats |
| GET | `/api/v1/stats/history` | Historical stats for dashboard (time-series data) |

**GET /api/v1/stats/global** (Sample Response)
```json
{
  "timestamp": "2026-01-02T12:00:00Z",
  "total_ips": 50000,
  "allocated_ips": 12500,
  "reserved_ips": 500,
  "active_leases": 8750,
  "utilization_percent": 25,
  "errors_24h": 5,
  "scopes": [
    {
      "scope_id": "scope_aAbBcCdDeEfF123456",
      "name": "web-tier-zone-a",
      "utilization_percent": 78.1,
      "active_leases": 3500
    }
  ]
}
```

### 5.18 Helper Endpoints (Next Available)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/ipam/next-available-subnet` | Get next available subnet within a prefix |
| GET | `/api/v1/ipam/next-available-ip` | Get next available IP within a pool |

**GET /api/v1/ipam/next-available-subnet?prefix_id=prefix_xyz&size=24** (Sample Response)
```json
{
  "subnet": "10.1.5.0/24",
  "parent_prefix_id": "prefix_xyz",
  "available": true
}
```

**GET /api/v1/ipam/next-available-ip?pool_id=pool_xyz** (Sample Response)
```json
{
  "ip": "10.1.0.105",
  "pool_id": "pool_xyz",
  "available": true
}
```

### 5.19 Live Updates Stream (Server-Sent Events)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/stream/updates` | SSE stream for live dashboard updates |

**GET /api/v1/stream/updates?events=scope,pool,lease,stat**

Sends server-sent events for subscribed entity types. Useful for live dashboards.

Example SSE messages:
```
event: scope
data: {"action":"updated","id":"scope_xyz","timestamp":"2026-01-02T12:00:01Z"}

event: lease
data: {"action":"created","id":"lease_abc","pool_id":"pool_xyz","ip":"10.1.0.105"}

event: stat
data: {"type":"scope","id":"scope_xyz","utilization_percent":78.5}
```

Query parameters:
- `events` (optional, comma-separated): subscribe to specific types (scope, pool, lease, reservation, server, stat)
- Default: subscribe to all types

### 5.20 System Health & Readiness

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/health` | Simple liveness check (basic connectivity) |
| GET | `/api/v1/ready` | Readiness check (database, dependencies) |

**GET /api/v1/health** (200 OK)
```json
{
  "status": "ok"
}
```

**GET /api/v1/ready** (200 OK)
```json
{
  "status": "ready",
  "database": "connected",
  "cache": "connected",
  "timestamp": "2026-01-02T12:00:00Z"
}
```

Returns 503 if any dependency is unavailable.

---

## 6. CLI (Command-Line Interface)

### 6.1 Binary Structure Decision

**Single binary: `gdhcpd`** with subcommands.

Rationale:
- One artifact to distribute and manage
- Simpler deployment (single binary, no version mismatch)
- Config and token management unified
- Standard Go tool pattern (go, docker, kubectl, etc.)

Subcommands: `gdhcpd serve`, `gdhcpd migrate`, `gdhcpd vrf ...`, `gdhcpd scope ...`, etc.

### 6.2 Global Flags

Available on all commands:

```
--server <url>           API server URL (default: from config or http://localhost:8080)
--token <token>          API token (default: from config)
--output json|yaml|table Output format (default: table)
--config <path>          Config file path (default: ~/.gdhcp.yaml)
--verbose, -v            Enable verbose output
--debug                  Enable debug logging
--ca-cert <path>         Path to CA certificate for TLS verification
--insecure               Skip TLS certificate verification (dev only)
--timeout <duration>     Request timeout (default: 30s)
```

### 6.3 Config File (~/.gdhcp.yaml)

Created/updated by CLI on first use or via `gdhcp config init`:

```yaml
server: https://dhcp.example.com:8080
token: sk_abc123def456ghi789jkl012mno345
output: table
ca_cert: /etc/gdhcp/ca.crt
timeout: 30s

# Context management (optional, for switching between environments)
contexts:
  prod:
    server: https://dhcp-prod.example.com:8080
    token: sk_prod_xyz
  staging:
    server: https://dhcp-staging.example.com:8080
    token: sk_staging_abc
current_context: prod
```

### 6.4 CLI Command Tree

#### 6.4.1 VRF Commands
```
gdhcp vrf list                           # List VRFs (table output)
gdhcp vrf create --name prod             # Create VRF
gdhcp vrf create --from-file vrf.yaml    # Create from YAML
gdhcp vrf update <vrf_id> --name prod2   # Update VRF
gdhcp vrf delete <vrf_id>                # Delete VRF
gdhcp vrf get <vrf_id>                   # Get details (JSON output default with --output=json)
```

#### 6.4.2 Prefix Commands
```
gdhcp prefix list                                  # List prefixes
gdhcp prefix list --vrf prod                       # Filter by VRF
gdhcp prefix create --cidr 10.0.0.0/8 --name core # Create prefix
gdhcp prefix update <prefix_id> --name new-name   # Update prefix
gdhcp prefix delete <prefix_id>                    # Delete prefix
gdhcp prefix get <prefix_id>                       # Get details
gdhcp prefix children <prefix_id>                  # List children with utilization
```

#### 6.4.3 Subnet Commands
```
gdhcp subnet list                                        # List subnets
gdhcp subnet list --prefix <prefix_id>                   # Filter by prefix
gdhcp subnet create --cidr 10.1.0.0/24 --prefix <id>    # Create subnet
gdhcp subnet update <subnet_id> --name new-name         # Update subnet
gdhcp subnet delete <subnet_id>                          # Delete subnet
gdhcp subnet get <subnet_id>                             # Get details
```

#### 6.4.4 Scope Commands
```
gdhcp scope list                                         # List scopes
gdhcp scope list --vrf prod                              # Filter by VRF
gdhcp scope create --subnet <subnet_id> --name web-tier  # Create scope
gdhcp scope update <scope_id> --lease-time 86400         # Update scope
gdhcp scope delete <scope_id>                            # Delete scope
gdhcp scope get <scope_id>                               # Get details
gdhcp scope stats <scope_id>                             # Get utilization stats
gdhcp scope enable <scope_id>                            # Enable scope
gdhcp scope disable <scope_id>                           # Disable scope
```

#### 6.4.5 Pool Commands
```
gdhcp pool list                                    # List pools
gdhcp pool list --scope <scope_id>                 # Filter by scope
gdhcp pool create --scope <scope_id> --start-ip 10.1.0.10 --end-ip 10.1.0.200 # Create pool
gdhcp pool update <pool_id> --name new-pool        # Update pool
gdhcp pool delete <pool_id>                        # Delete pool
gdhcp pool get <pool_id>                           # Get details
gdhcp pool stats <pool_id>                         # Get pool stats
gdhcp pool enable <pool_id>                        # Enable pool
gdhcp pool disable <pool_id>                       # Disable pool
```

#### 6.4.6 Reservation Commands (under Pool per Product Requirement)
```
gdhcp pool <pool_id> reservation list                                    # List reservations
gdhcp pool <pool_id> reservation add --mac 00:11:22:33:44:55 --ip 10.1.0.50 # Add reservation
gdhcp pool <pool_id> reservation update <reservation_id> --hostname web-01 # Update reservation
gdhcp pool <pool_id> reservation delete <reservation_id>                 # Delete reservation
gdhcp pool <pool_id> reservation get <reservation_id>                    # Get details
```

#### 6.4.7 Lease Commands
```
gdhcp lease list                         # List leases
gdhcp lease list --pool <pool_id>        # Filter by pool
gdhcp lease list --scope <scope_id>      # Filter by scope
gdhcp lease get <lease_id>               # Get details
gdhcp lease release <lease_id>           # Release lease
gdhcp lease wake <lease_id>              # Send WoL packet
gdhcp lease search --ip 10.1.0.100       # Search by IP
gdhcp lease search --mac aa:bb:cc:dd:ee:ff # Search by MAC
gdhcp lease search --hostname web-server # Search by hostname
```

#### 6.4.8 Template Commands
```
gdhcp template list                              # List templates
gdhcp template create --name web-std --from-file template.yaml # Create template
gdhcp template update <template_id> --name new-name # Update template
gdhcp template delete <template_id>              # Delete template
gdhcp template get <template_id>                 # Get details
gdhcp template apply <template_id> --scope <scope_id> # Apply template to scope
```

#### 6.4.9 Option Commands
```
gdhcp option catalog                     # List all IANA DHCP options
gdhcp option catalog --search dns        # Search catalog
gdhcp option list                        # List custom options
gdhcp option create --code 200 --name custom --type string # Create custom option
gdhcp option update <option_id> --name new-name # Update option
gdhcp option delete <option_id>          # Delete option
gdhcp option get <option_id>             # Get details

# Option values (scope-level)
gdhcp scope <scope_id> option set --code 6 --value "8.8.8.8,8.8.4.4" # Set option
gdhcp scope <scope_id> option get --code 6                             # Get option value
gdhcp scope <scope_id> option list                                     # List all options

# Option values (pool-level)
gdhcp pool <pool_id> option set --code 3 --value "10.1.0.1"  # Set option
gdhcp pool <pool_id> option get --code 3                      # Get option value
gdhcp pool <pool_id> option list                              # List all options
```

#### 6.4.10 Cluster Commands
```
gdhcp cluster list                          # List clusters
gdhcp cluster create --name prod --vip 10.0.0.1 # Create cluster
gdhcp cluster update <cluster_id> --name new    # Update cluster
gdhcp cluster delete <cluster_id>              # Delete cluster
gdhcp cluster get <cluster_id>                 # Get details
gdhcp cluster servers <cluster_id>             # List servers in cluster
```

#### 6.4.11 Server Commands
```
gdhcp server list                                           # List all servers
gdhcp server list --cluster <cluster_id>                    # Filter by cluster
gdhcp server create --cluster <cluster_id> --hostname dhcp-01 # Create server
gdhcp server update <server_id> --ip 10.0.0.12             # Update server
gdhcp server delete <server_id>                             # Delete server
gdhcp server get <server_id>                                # Get details
gdhcp server promote <server_id>                            # Make primary
```

#### 6.4.12 User Commands (Admin)
```
gdhcp user list                          # List users
gdhcp user create --username alice --roles operator # Create user
gdhcp user update <user_id> --roles admin,operator # Update user roles
gdhcp user delete <user_id>              # Delete user
gdhcp user get <user_id>                 # Get details
gdhcp user set-password <user_id>        # Set password (interactive prompt)
gdhcp user enable <user_id>              # Enable user
gdhcp user disable <user_id>             # Disable user
```

#### 6.4.13 Token Commands
```
gdhcp token list                                  # List user's API tokens
gdhcp token create --name ci-automation         # Create new token
gdhcp token revoke <token_id>                    # Revoke token
gdhcp token list --user <user_id>                # List tokens for another user (admin)
```

#### 6.4.14 Tag Commands
```
gdhcp tag list                           # List tags
gdhcp tag create --name production --color ff5722 # Create tag
gdhcp tag update <tag_id> --name prod2   # Update tag
gdhcp tag delete <tag_id>                # Delete tag
gdhcp tag get <tag_id>                   # Get details
```

#### 6.4.15 IPAM Commands
```
gdhcp ipam tree                              # Show full IPAM tree
gdhcp ipam tree --prefix <prefix_id>         # Show subtree
gdhcp ipam search --ip 10.1.0.50             # Search by IP
gdhcp ipam search --mac aa:bb:cc:dd:ee:ff    # Search by MAC
gdhcp ipam search --hostname web-server      # Search by hostname
gdhcp ipam search --tag production           # Search by tag
gdhcp ipam next-subnet --prefix <prefix_id> --size 24 # Get next available subnet
gdhcp ipam next-ip --pool <pool_id>          # Get next available IP
```

#### 6.4.16 Stats Commands
```
gdhcp stats global                      # Global utilization stats
gdhcp stats cluster <cluster_id>        # Cluster-level stats
gdhcp stats scope <scope_id>            # Scope-level stats
gdhcp stats pool <pool_id>              # Pool-level stats
gdhcp stats history --days 7            # Historical stats (time-series)
```

#### 6.4.17 Auth Commands
```
gdhcp auth login                         # Interactive login (stores JWT session)
gdhcp auth logout                        # Remove stored session
gdhcp auth me                            # Show current user info
gdhcp auth token create --name automation # Create API token for current user
```

#### 6.4.18 Config Commands
```
gdhcp config init                        # Interactive config setup
gdhcp config set server https://dhcp.example.com:8080 # Set config value
gdhcp config get server                  # Get config value
gdhcp config show                        # Show full config
gdhcp config context create prod --server https://prod.example.com # Create context
gdhcp config context switch prod         # Switch to context
gdhcp config context list                # List contexts
```

#### 6.4.19 Server Subcommand
```
gdhcpd serve [flags]                     # Run DHCP server
gdhcpd migrate [up|down|status] [flags]  # Database migrations
```

### 6.5 Output Formats

#### Table Format (Default)
```
$ gdhcp scope list
NAME              SUBNET          ENABLED  LEASE_TIME  STATUS
web-tier-zone-a   10.1.0.0/24     yes      86400       healthy
api-tier-zone-b   10.2.0.0/24     yes      86400       healthy
```

#### JSON Format
```
$ gdhcp scope list --output json
[
  {
    "id": "scope_xyz",
    "name": "web-tier-zone-a",
    "subnet_id": "subnet_abc",
    "enabled": true,
    "lease_time": 86400,
    "status": "healthy"
  }
]
```

#### YAML Format
```
$ gdhcp scope list --output yaml
- id: scope_xyz
  name: web-tier-zone-a
  subnet_id: subnet_abc
  enabled: true
  lease_time: 86400
  status: healthy
```

### 6.6 Shell Completion

Bash/Zsh completion generated at install time:

```bash
eval "$(gdhcpd completion bash)"   # bash
eval "$(gdhcpd completion zsh)"    # zsh
```

Completion for:
- Subcommands (vrf, scope, pool, etc.)
- Flags (--output, --token, etc.)
- Resource IDs (tab-complete from API on supported commands)
- Dynamic completions (scopes by VRF, pools by scope, etc.)

---

## 7. Database Schema Overview

Entity tables (schema owned by migration system; CLI/API read-only):

| Table | Purpose |
|-------|---------|
| `vrfs` | Virtual routing/forwarding contexts |
| `prefixes` | IP space aggregates |
| `subnets` | Child allocations; may/may not have DHCP scope |
| `scopes` | DHCP scopes (1:1 with subnet) |
| `pools` | IP pools within scopes |
| `reservations` | Fixed IP assignments within pools |
| `leases` | Active DHCP leases |
| `templates` | Reusable DHCP settings |
| `options` | DHCP option definitions (standard + custom) |
| `option_values` | Option assignments to scopes/pools |
| `clusters` | HA cluster groups |
| `servers` | DHCP server nodes |
| `users` | Administrative users |
| `api_tokens` | Personal access tokens |
| `tags` | Metadata labels |
| `audit_logs` | Change audit trail |

---

## 8. Error Scenarios & Handling

### IP Overlap Detection
- Prevent subnet/pool creation if CIDR overlaps existing (within same VRF)
- API returns 409 Conflict with details
- CLI shows user-friendly error

### Reservation Conflicts
- Prevent reservation creation if IP already leased or reserved
- Return 409 Conflict with conflicting entity info

### Scope Deletion
- Require all pools to be deleted first (or cascade delete with confirmation flag)
- Return 409 Conflict if pools exist and --cascade not provided

### Circular Template Dependencies
- Prevent templates from including themselves or creating cycles
- Return 400 Invalid Input

---

## 9. Future Extensibility

Designed for:
- DHCPv6 support (separate scope/pool objects per version)
- Additional entity types (subnets, additional option types)
- Audit logging and compliance features
- API rate limiting and quotas
- Advanced search/filtering (Elasticsearch-style queries)
- Webhook/callback integrations for external systems
- Terraform provider support (based on API structure)

---

## 10. Implementation Notes

### Code Organization (Go Backend)
```
/cmd/gdhcpd
  - main.go (entrypoint, subcommand routing)
  - serve.go (HTTP server startup)
  - migrate.go (database migrations)

/api
  - router.go (REST endpoint registration)
  - handlers/ (endpoint handlers, CRUD)
    - vrf.go, scope.go, pool.go, etc.
  - middleware/ (auth, logging, error handling)
  - types.go (request/response types)

/internal
  - db/ (database queries, migrations)
  - ipam/ (IP allocation logic)
  - dhcp/ (DHCP server logic)
  - auth/ (JWT, token validation)
  - config/ (configuration loading)

/cli
  - main.go (cobra root command setup)
  - commands/ (cobra commands)
    - vrf.go, scope.go, pool.go, etc.
  - output/ (formatting: json, yaml, table)
```

### Go Modules
- `github.com/gorilla/mux` (HTTP routing)
- `github.com/spf13/cobra` (CLI)
- `github.com/spf13/viper` (config management)
- `github.com/lib/pq` (PostgreSQL driver)
- `jwt-go` (JWT token management)
- `gopkg.in/yaml.v3` (YAML serialization)
- `github.com/jmoiron/sqlc` (type-safe SQL queries)

### Embedding Frontend
- Angular dist built to `/frontend/dist`
- Embedded via `go:embed` in `/frontend/embedded.go`
- Served statically at `/` via middleware
- API requests proxied to `/api/v1` handlers

---

## 11. Examples

### Creating a Scope and Pool via API
```bash
# Create VRF
curl -X POST https://dhcp.example.com/api/v1/vrfs \
  -H "Authorization: Bearer $JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "production",
    "description": "Production network"
  }'

# Create prefix
curl -X POST https://dhcp.example.com/api/v1/prefixes \
  -H "Authorization: Bearer $JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "vrf_id": "vrf_xyz",
    "cidr": "10.0.0.0/8",
    "name": "Core"
  }'

# Create subnet
curl -X POST https://dhcp.example.com/api/v1/subnets \
  -H "Authorization: Bearer $JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "prefix_id": "prefix_abc",
    "cidr": "10.1.0.0/24",
    "name": "Web Tier"
  }'

# Create scope (1:1 with subnet)
curl -X POST https://dhcp.example.com/api/v1/scopes \
  -H "Authorization: Bearer $JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "subnet_id": "subnet_def",
    "name": "web-tier-dhcp",
    "lease_time": 86400
  }'

# Create pool (within scope)
curl -X POST https://dhcp.example.com/api/v1/pools \
  -H "Authorization: Bearer $JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "scope_id": "scope_ghi",
    "name": "dynamic-pool-1",
    "start_ip": "10.1.0.10",
    "end_ip": "10.1.0.200"
  }'

# Add reservation within pool
curl -X POST https://dhcp.example.com/api/v1/pools/pool_jkl/reservations \
  -H "Authorization: Bearer $JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "mac_address": "00:11:22:33:44:55",
    "ip_address": "10.1.0.50",
    "hostname": "web-server-01"
  }'
```

### Creating via CLI
```bash
# Setup config
gdhcp config init
# Prompts: server URL, create API token, output format

# Create VRF
gdhcp vrf create --name production --description "Production network"

# Create prefix
gdhcp prefix create --cidr 10.0.0.0/8 --vrf production --name Core

# Create subnet
gdhcp subnet create --cidr 10.1.0.0/24 --prefix <prefix_id> --name "Web Tier"

# Create scope
gdhcp scope create --subnet <subnet_id> --name web-tier-dhcp --lease-time 86400

# Create pool
gdhcp pool create --scope <scope_id> --name dynamic-pool-1 --start-ip 10.1.0.10 --end-ip 10.1.0.200

# Add reservation
gdhcp pool <pool_id> reservation add --mac 00:11:22:33:44:55 --ip 10.1.0.50 --hostname web-server-01

# View IPAM tree
gdhcp ipam tree

# Search
gdhcp ipam search --ip 10.1.0.50
gdhcp ipam search --hostname web-server
gdhcp ipam search --tag production

# View stats
gdhcp stats global
gdhcp stats scope <scope_id>
gdhcp stats pool <pool_id>

# List leases
gdhcp lease list --scope <scope_id>
gdhcp lease release <lease_id>
```

---

## 12. Deprecation & Versioning

- API versioned at `/api/v1` in URL
- Future versions (v2, v3) run alongside v1
- Deprecations announced with `Deprecation` response header and `deprecated` field in schema
- CLI auto-detects API version; warns if using deprecated endpoints

---

## Summary Table: REST Endpoints by Resource

| Resource | List | Create | Get | Update | Delete | Custom |
|----------|------|--------|-----|--------|--------|--------|
| VRF | GET `/vrfs` | POST `/vrfs` | GET `/vrfs/{id}` | PUT `/vrfs/{id}` | DELETE `/vrfs/{id}` | - |
| Prefix | GET `/prefixes` | POST `/prefixes` | GET `/prefixes/{id}` | PUT `/prefixes/{id}` | DELETE `/prefixes/{id}` | GET `/prefixes/{id}/children` |
| Subnet | GET `/subnets` | POST `/subnets` | GET `/subnets/{id}` | PUT `/subnets/{id}` | DELETE `/subnets/{id}` | - |
| Scope | GET `/scopes` | POST `/scopes` | GET `/scopes/{id}` | PUT `/scopes/{id}` | DELETE `/scopes/{id}` | GET `/scopes/{id}/stats`, GET `/scopes/{id}/pools` |
| Pool | GET `/pools` | POST `/pools` | GET `/pools/{id}` | PUT `/pools/{id}` | DELETE `/pools/{id}` | GET `/pools/{id}/stats`, GET/POST `/pools/{id}/reservations` |
| Reservation | GET via pool | POST via pool | GET via pool | PUT via pool | DELETE via pool | (Always via pool) |
| Lease | GET `/leases` | - | GET `/leases/{id}` | - | - | POST `/leases/{id}/release`, POST `/leases/{id}/wake` |
| Template | GET `/templates` | POST `/templates` | GET `/templates/{id}` | PUT `/templates/{id}` | DELETE `/templates/{id}` | - |
| Option | GET `/options` | POST `/options` | GET `/options/{id}` | PUT `/options/{id}` | DELETE `/options/{id}` | GET `/options/catalog` |
| Cluster | GET `/clusters` | POST `/clusters` | GET `/clusters/{id}` | PUT `/clusters/{id}` | DELETE `/clusters/{id}` | GET `/clusters/{id}/servers` |
| Server | GET `/servers` | POST `/servers` | GET `/servers/{id}` | PUT `/servers/{id}` | DELETE `/servers/{id}` | - |
| User | GET `/users` | POST `/users` | GET `/users/{id}` | PUT `/users/{id}` | DELETE `/users/{id}` | GET/POST `/users/{id}/tokens` |
| Tag | GET `/tags` | POST `/tags` | GET `/tags/{id}` | PUT `/tags/{id}` | DELETE `/tags/{id}` | - |
| IPAM | - | - | - | - | - | GET `/ipam/tree`, GET `/ipam/next-available-subnet`, GET `/ipam/next-available-ip` |
| Search | - | - | - | - | - | GET `/search` |
| Stats | - | - | - | - | - | GET `/stats/global`, `/stats/cluster/{id}`, `/stats/scope/{id}`, `/stats/pool/{id}`, `/stats/history` |
| Auth | - | - | - | - | - | POST `/auth/login`, POST `/auth/refresh`, POST `/auth/logout`, GET `/auth/me` |
| Health | - | - | - | - | - | GET `/health`, GET `/ready` |
| Stream | - | - | - | - | - | GET `/stream/updates` (SSE) |

---

END OF DESIGN DOCUMENT
