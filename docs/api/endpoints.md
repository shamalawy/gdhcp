# gdhcp REST API Endpoints (Complete Reference)

**Base URL**: `/api/v1`  
**Authentication**: `Authorization: Bearer <token>` (JWT or API token)  
**Content-Type**: `application/json`

---

## Authentication Endpoints

| Method | Path | Params | Request | Response | Status | Notes |
|--------|------|--------|---------|----------|--------|-------|
| POST | `/auth/login` | - | `{username, password}` | `{token, expires_at}` | 200/401 | Session JWT (24h TTL) |
| POST | `/auth/logout` | - | - | `{message}` | 200 | Revoke current session |
| POST | `/auth/refresh` | - | `{refresh_token}` | `{token, expires_at}` | 200/401 | New JWT from refresh token |

---

## User Management Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/users` | `limit, offset, sort, search` | - | `[User]` + pagination | 200 | List users (admin only) |
| GET | `/users/{user_id}` | - | - | `User` | 200/404 | Get single user |
| POST | `/users` | - | `{username, email, password, description}` | `User` | 201/400 | Create user (admin only) |
| PUT | `/users/{user_id}` | - | `{username, email, description, is_active}` | `User` | 200/400/404 | Update user (admin only) |
| DELETE | `/users/{user_id}` | - | - | `{message}` | 204/404 | Delete user (admin only) |
| GET | `/users/{user_id}/tokens` | `limit, offset` | - | `[APIToken]` | 200 | List tokens for user |
| POST | `/users/{user_id}/tokens` | - | `{description, expires_at}` | `{token, id, ...}` | 201 | Create API token (raw token shown once) |
| DELETE | `/users/{user_id}/tokens/{token_id}` | - | - | `{message}` | 204 | Revoke API token |

---

## VRF Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/vrfs` | `limit, offset, sort, search` | - | `[VRF]` + pagination | 200 | List VRFs |
| GET | `/vrfs/{vrf_id}` | - | - | `VRF` | 200/404 | Get single VRF |
| POST | `/vrfs` | - | `{name, description, route_distinguisher}` | `VRF` | 201/400 | Create VRF |
| PUT | `/vrfs/{vrf_id}` | - | `{name, description, route_distinguisher}` | `VRF` | 200/400/404 | Update VRF |
| DELETE | `/vrfs/{vrf_id}` | - | - | `{message}` | 204/404 | Delete VRF (no scopes) |

---

## IPAM Prefix Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/prefixes` | `limit, offset, sort, search, vrf_id, type` | - | `[IPAMPrefix]` + pag | 200 | List prefixes |
| GET | `/prefixes/{prefix_id}` | - | - | `IPAMPrefix` | 200/404 | Get single prefix |
| POST | `/prefixes` | - | `{vrf_id, cidr, type, name, description}` | `IPAMPrefix` | 201/400/409 | Create prefix (no overlap at subnet level) |
| PUT | `/prefixes/{prefix_id}` | - | `{name, description, type}` | `IPAMPrefix` | 200/400/404 | Update prefix |
| DELETE | `/prefixes/{prefix_id}` | - | - | `{message}` | 204/404 | Delete prefix (no children) |
| GET | `/prefixes/{prefix_id}/clients` | `limit, offset, sort, search, kind, state` | - | `[DHCPClient]` + pag | 200 | List leases + reservations in prefix and descendants |
| GET | `/ipam/tree` | `vrf_id, depth, parent_id` | - | Tree structure | 200 | Hierarchical tree (lazy-loaded) |
| GET | `/search` | `q, entity_types, limit` | - | `[SearchResult]` | 200 | Global search across entities (see below) |

### Global search

`q` is matched two ways at once: structurally when it parses as an IP or CIDR (prefix and
scope containment, pool range membership, lease/reservation/VIP address equality), and as
a case-insensitive substring against names, descriptions, hostnames, address text,
reservation binding keys, hex lease client IDs, VLAN IDs, option codes and tag names.
LIKE wildcards in `q` are escaped and match literally.

`entity_types` is a comma-separated filter over: `vrf`, `prefix`, `vlan`, `vlan_group`,
`scope`, `pool`, `reservation`, `lease`, `template`, `option`, `cluster`. `user` and
`server` are never returned — this route is viewer-accessible and the user directory is
admin-only (amendment A15).

A `SearchResult` is `{type, entity_id, title, subtitle?, description?, url}`. **`url` is a
frontend route** and is guaranteed to resolve in the SPA (amendment A18.2 lists the
mapping); results are ranked exact-title, then title-prefix, then entity kind, with each
kind capped at `max(5, limit/5)` rows so one noisy kind cannot crowd out the rest. A query
with no matches returns `[]`, never `null`.

---

## DHCP Scope Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/scopes` | `limit, offset, sort, search, vrf_id, cluster_id` | - | `[DHCPScope]` + pag | 200 | List scopes |
| GET | `/scopes/{scope_id}` | - | - | `DHCPScope` | 200/404 | Get scope with options |
| POST | `/scopes` | - | `{vrf_id, cluster_id, ipam_prefix_id, name, description}` | `DHCPScope` | 201/400 | Create scope |
| PUT | `/scopes/{scope_id}` | - | `{name, description, is_enabled}` | `DHCPScope` | 200/400/404 | Update scope |
| DELETE | `/scopes/{scope_id}` | - | - | `{message}` | 204/404 | Delete scope (cascade pools) |
| GET | `/scopes/{scope_id}/utilization` | - | - | `{total, active, reserved, pct}` | 200 | Current utilization (computed) |
| GET | `/scopes/{scope_id}/stats` | `window` | - | `DHCPStats` | 200 | Transaction stats |

---

## DHCP Pool Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/scopes/{scope_id}/pools` | `limit, offset, sort` | - | `[DHCPPool]` + pag | 200 | List pools in scope |
| GET | `/pools/{pool_id}` | - | - | `DHCPPool` | 200/404 | Get pool |
| POST | `/scopes/{scope_id}/pools` | - | `{name, range_start, range_end, priority}` | `DHCPPool` | 201/400 | Create pool (range in scope) |
| PUT | `/pools/{pool_id}` | - | `{name, range_start, range_end, priority, is_enabled}` | `DHCPPool` | 200/400/404 | Update pool |
| DELETE | `/pools/{pool_id}` | - | - | `{message}` | 204/404 | Delete pool (cascade leases) |
| GET | `/pools/{pool_id}/utilization` | - | - | `{total, active, reserved, pct}` | 200 | Pool utilization |

---

## Reservation Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/pools/{pool_id}/reservations` | `limit, offset, sort, search` | - | `[DHCPReservation]` + pag | 200 | List reservations in pool |
| GET | `/reservations/{reservation_id}` | - | - | `DHCPReservation` | 200/404 | Get reservation |
| POST | `/pools/{pool_id}/reservations` | - | `{binding_type, binding_key, reserved_ip, hostname, description}` | `DHCPReservation` | 201/400 | Create reservation (IP in pool) |
| PUT | `/reservations/{reservation_id}` | - | `{hostname, description, is_enabled, binding_key, binding_type}` | `DHCPReservation` | 200/400/404 | Update reservation |
| DELETE | `/reservations/{reservation_id}` | - | - | `{message}` | 204/404 | Delete reservation |

**Binding key contract** (all endpoints): Seven binding types are supported; both `binding_type` and `binding_key` are required at creation time. The handler accepts raw client identifiers (e.g., MAC address as `aa:bb:cc:dd:ee:ff` for type `mac`, or text for type `hostname`) and stores them in canonical prefixed form (e.g., `mac:aa:bb:cc:dd:ee:ff`, `hostname:printer`). GET responses return the canonical key, and an already-prefixed key is accepted without being double-prefixed. The five opaque types (`client_id`, `circuit_id`, `remote_id`, `vendor_class`, `user_class`) are stored as lowercase hex: a value that already looks like hex (an even number of hex digits) is taken as hex, anything else is hex-encoded as UTF-8, so `circuit_id` may be sent as `eth 1/0/24`. Invalid input (a malformed MAC, an unknown `binding_type`) returns 400 with the offending field. See AMENDMENTS.md A17 for canonical forms and matching precedence.

---

## Lease Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/leases` | `limit, offset, cursor, sort, search, scope_id, pool_id, state` | - | `[DHCPLease]` + pag | 200 | List all leases (filterable). `search` matches IP, MAC or hostname |
| GET | `/leases/{lease_id}` | - | - | `DHCPLease` | 200/404 | Get lease |
| GET | `/pools/{pool_id}/leases` | `limit, offset, search, state` | - | `[DHCPLease]` + pag | 200 | List leases in pool |
| GET | `/leases/by-ip/{ip_address}` | - | - | `DHCPLease` | 200/404 | Get lease by IP |
| GET | `/leases/by-mac/{mac_address}` | - | - | `[DHCPLease]` | 200 | Get leases by MAC |
| POST | `/leases/{lease_id}/release` | - | - | `{message}` | 200/404 | Release: address returns to the pool |
| POST | `/leases/{lease_id}/abandon` | - | - | `{message}` | 200/404 | Abandon: in use on the wire, held back from reallocation |
| POST | `/leases/{lease_id}/renew` | - | `{duration_seconds?}` | `DHCPLease` | 200/400/404 | Force renew; reactivates a lapsed lease |
| DELETE | `/leases/{lease_id}` | - | - | `{message}` | 200/404 | Delete the lease record |
| POST | `/leases/bulk` | - | `{action, lease_ids, duration_seconds?}` | `LeaseBulkResponse` | 200/400 | One action over up to 500 leases |

**Reads** are `RoleViewer`; **the five action routes are `RoleOperator`** and audited under
entity type `lease` (see amendment A18.1). Allocation and renewal on the wire remain the
DHCP engine's own business — these routes are the operator's override.

`POST /leases/{lease_id}/renew` with no body, or `duration_seconds: 0`, reuses the lease's
own original duration (`lease_end - lease_start`), falling back to 24h.

`POST /leases/bulk` takes `action` of `release`, `abandon`, `renew` or `delete`. It is
**not transactional**: it answers `200` with a per-lease breakdown so a partially
successful action is reportable rather than rolled back.

```json
{
  "action": "release",
  "succeeded": 2,
  "failed": 1,
  "results": [
    { "lease_id": "3f1c…", "ok": true },
    { "lease_id": "8a20…", "ok": true },
    { "lease_id": "0000…", "ok": false, "error": "lease not found" }
  ]
}
```

---

## DHCP Option Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/options` | `limit, offset, sort, standard, custom` | - | `[DHCPOption]` | 200 | List custom/standard options |
| GET | `/options/catalog` | - | - | `[OptionCatalogEntry]` | 200 | List all IANA options (no paging) |
| GET | `/options/{option_id}` | - | - | `DHCPOption` | 200/404 | Get option |
| POST | `/options` | - | `{name, description, code, value_type}` | `DHCPOption` | 201/400 | Create custom option |
| DELETE | `/options/{option_id}` | - | - | `{message}` | 204/404 | Delete custom option |
| GET | `/scopes/{scope_id}/options` | - | - | `[OptionValue]` | 200 | Get scope options |
| POST | `/scopes/{scope_id}/options` | - | `{option_code, value}` | `OptionValue` | 201/400 | Set scope option |
| PUT | `/scopes/{scope_id}/options/{option_id}` | - | `{value, is_enabled}` | `OptionValue` | 200/400 | Update scope option |
| DELETE | `/scopes/{scope_id}/options/{option_id}` | - | - | `{message}` | 204 | Remove scope option |
| GET | `/pools/{pool_id}/options` | - | - | `[OptionValue]` | 200 | Get pool options |
| POST | `/pools/{pool_id}/options` | - | `{option_code, value}` | `OptionValue` | 201 | Set pool option |
| DELETE | `/pools/{pool_id}/options/{option_id}` | - | - | `{message}` | 204 | Remove pool option |

---

## Template Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/templates` | `limit, offset, sort, search` | - | `[DHCPTemplate]` | 200 | List templates |
| GET | `/templates/{template_id}` | - | - | `DHCPTemplate` | 200/404 | Get template |
| POST | `/templates` | - | `{name, description}` | `DHCPTemplate` | 201/400 | Create template |
| PUT | `/templates/{template_id}` | - | `{name, description, is_enabled}` | `DHCPTemplate` | 200/400/404 | Update template |
| DELETE | `/templates/{template_id}` | - | - | `{message}` | 204/404 | Delete template |
| GET | `/templates/{template_id}/options` | - | - | `[OptionValue]` | 200 | Get template options |
| POST | `/templates/{template_id}/options` | - | `{option_code, value}` | `OptionValue` | 201 | Set template option |
| GET | `/templates/{template_id}/scopes` | - | - | `[TemplateScopeAssoc]` | 200 | List scopes using template |
| POST | `/templates/{template_id}/scopes/{scope_id}` | - | `{priority}` | `TemplateScopeAssoc` | 201 | Associate template to scope |
| DELETE | `/templates/{template_id}/scopes/{scope_id}` | - | - | `{message}` | 204 | Disassociate template from scope |

---

## VLAN Endpoints

See AMENDMENTS.md A16.1. VLANs record layer-2 segmentation; `ipam_prefixes.vlan_id`
assigns one to a prefix.

| Method | Path | Notes |
|---|---|---|
| GET | `/vlans` | Filters: `group_id` (`none` = ungrouped), `status`. Search matches name or VID. |
| GET | `/vlans/{vlan_id}` | Includes joined `group_name` and `prefix_count`. |
| GET | `/vlans/{vlan_id}/prefixes` | IPAM prefixes assigned to this VLAN. |
| POST | `/vlans` | `{vid, name, group_id?, description?, status?}`. VID must be 1-4094 and inside the group's range. |
| PUT | `/vlans/{vlan_id}` | Same body; `vid` optional. |
| DELETE | `/vlans/{vlan_id}` | Assigned prefixes are unassigned, not deleted. |
| GET | `/vlan-groups` | |
| GET | `/vlan-groups/{group_id}` | |
| POST | `/vlan-groups` | `{name, description?, vrf_id?, min_vid?, max_vid?}`, defaults 1-4094. |
| PUT | `/vlan-groups/{group_id}` | |
| DELETE | `/vlan-groups/{group_id}` | Member VLANs survive as ungrouped. |

## IPAM Helper Endpoints

| Method | Path | Notes |
|---|---|---|
| GET | `/prefixes/{prefix_id}/utilization` | `models.PrefixUtilization` — DHCP view + address-space view (AMENDMENTS A16.3). |
| GET | `/prefixes/{prefix_id}/ancestors` | Root-first ancestor chain, for breadcrumbs and tree reveal. |
| GET | `/ipam/next-available-subnet?prefix_id=&size=` | `{subnet, parent_prefix_id, available}`. Out of space is `available: false` with HTTP 200, not an error. |

## Tag Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/tags` | `limit, offset, sort, search` | - | `[Tag]` | 200 | List tags |
| POST | `/tags` | - | `{name, description, color}` | `Tag` | 201/400 | Create tag |
| DELETE | `/tags/{tag_id}` | - | - | `{message}` | 204/404 | Delete tag |
| POST | `/tags/{tag_id}/entities` | - | `{entity_type, entity_id}` | `EntityTag` | 201 | Add tag to entity |
| DELETE | `/tags/{tag_id}/entities/{entity_type}/{entity_id}` | - | - | `{message}` | 204 | Remove tag from entity |

---

## Cluster & HA Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/clusters` | `limit, offset, sort` | - | `[Cluster]` | 200 | List clusters |
| GET | `/clusters/{cluster_id}` | - | - | `Cluster` | 200/404 | Get cluster config |
| POST | `/clusters` | - | `{name, vip_address, vip_interface, heartbeat_interval_ms, ...}` | `Cluster` | 201/400 | Create cluster |
| PUT | `/clusters/{cluster_id}` | - | `{name, heartbeat_interval_ms, ...}` | `Cluster` | 200/400/404 | Update cluster config |
| DELETE | `/clusters/{cluster_id}` | - | - | `{message}` | 204/404 | Delete cluster (no servers) |
| GET | `/clusters/{cluster_id}/servers` | - | - | `[ClusterServer]` | 200 | List servers in cluster |
| POST | `/clusters/{cluster_id}/servers` | - | `{node_name, node_id, api_address, dhcp_address}` | `ClusterServer` | 201/400 | Register server (manual) |
| DELETE | `/clusters/{cluster_id}/servers/{server_id}` | - | - | `{message}` | 204 | Deregister server |
| GET | `/clusters/{cluster_id}/health` | - | - | `ClusterHealth` | 200 | Cluster health status |
| GET | `/clusters/{cluster_id}/heartbeats` | - | - | `[Heartbeat]` | 200 | All heartbeat records |
| POST | `/clusters/{cluster_id}/failover` | - | `{target_server_id}` | `{message, new_primary}` | 200 | Manual failover (admin) |
| POST | `/clusters/{cluster_id}/bootstrap` | - | `{server_ids: [...]}` | `{message}` | 201 | Initialize new cluster |

---

## Dashboard Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/dashboard/stats` | `window` | - | `DashboardStats` | 200 | Overall utilization + health |
| GET | `/dashboard/utilization-chart` | `entity_type, entity_id, days` | - | `[ChartPoint]` | 200 | Historical utilization trend |
| GET | `/dashboard/dhcp-stats` | `scope_id` | - | `DHCPStats` | 200 | DHCP transaction stats |
| GET | `/health` | - | - | `{status, db, ha, version}` | 200/503 | Server health check |

---

## Audit & Admin Endpoints

| Method | Path | Query | Request | Response | Status | Notes |
|--------|------|-------|---------|----------|--------|-------|
| GET | `/audit/log` | `limit, offset, sort, filter, entity_type, entity_id` | - | `[AuditLog]` | 200 | Audit log (read-only) |
| GET | `/config` | - | - | `{api_addr, ha_enabled, ...}` | 200 | Running config (read-only) |
| POST | `/config/reload` | - | `{source}` | `{message}` | 200 | Reload config from DB |

---

## Response Shape Reference

### DHCPScope (example)
```json
{
  "id": "uuid",
  "vrf_id": "uuid",
  "cluster_id": "uuid",
  "ipam_prefix_id": "uuid",
  "name": "production-web",
  "description": "Web tier DHCP",
  "network": "10.1.0.0/24",
  "is_enabled": true,
  "created_at": "2026-08-29T10:00:00Z",
  "updated_at": "2026-08-29T10:30:00Z"
}
```

### DHCPLease (example)
```json
{
  "id": "uuid",
  "pool_id": "uuid",
  "ip_address": "10.1.0.101",
  "client_mac": "aa:bb:cc:dd:ee:ff",
  "client_hostname": "laptop-001",
  "binding_state": "active",
  "binding_server_id": "server-uuid",
  "lease_start": "2026-08-29T10:00:00Z",
  "lease_end": "2026-08-30T10:00:00Z",
  "renewal_time": "2026-08-29T22:00:00Z",
  "rebinding_time": "2026-08-30T07:30:00Z",
  "created_at": "2026-08-29T10:00:00Z",
  "updated_at": "2026-08-29T10:30:00Z"
}
```

### DHCPClient (lease example — `GET /prefixes/{prefix_id}/clients`)
`binding_key`, `binding_type` and `is_enabled` belong to reservations and are null on a
lease. `client_id` is the raw option 61 value, so it is base64 in JSON (Go `[]byte`) and
is omitted when the client sent none.
```json
{
  "id": "uuid",
  "kind": "lease",
  "ip_address": "10.10.1.50",
  "pool_id": "uuid",
  "pool_name": "office-pool",
  "scope_id": "uuid",
  "scope_name": "office-lan",
  "prefix_id": "uuid",
  "client_mac": "aa:bb:cc:dd:ee:ff",
  "client_id": "Aaq7zN3u",
  "client_hostname": "laptop",
  "binding_state": "active",
  "lease_start": "2026-08-29T10:00:00Z",
  "lease_end": "2026-08-30T10:00:00Z",
  "binding_key": null,
  "binding_type": null,
  "is_enabled": null,
  "created_at": "2026-08-29T10:00:00Z",
  "updated_at": "2026-08-29T10:30:00Z"
}
```

### DHCPClient (reservation example — `GET /prefixes/{prefix_id}/clients`)
Lease timing and `binding_state` are null on a reservation. A MAC-bound reservation
reports its address in `client_mac` as well, so one table can render both kinds.
```json
{
  "id": "uuid",
  "kind": "reservation",
  "ip_address": "10.10.1.60",
  "pool_id": "uuid",
  "pool_name": "office-pool",
  "scope_id": "uuid",
  "scope_name": "office-lan",
  "prefix_id": "uuid",
  "client_mac": "aa:bb:cc:00:01:10",
  "client_hostname": "printer-hq-1",
  "binding_state": null,
  "lease_start": null,
  "lease_end": null,
  "binding_key": "mac:aa:bb:cc:00:01:10",
  "binding_type": "mac",
  "is_enabled": true,
  "created_at": "2026-08-29T10:00:00Z",
  "updated_at": "2026-08-29T10:00:00Z"
}
```

### ClusterHealth (example)
```json
{
  "cluster_id": "uuid",
  "local_server_id": "uuid",
  "local_role": "PRIMARY",
  "timestamp": "2026-08-29T10:30:00Z",
  "servers": [
    {
      "server_id": "uuid",
      "node_name": "dhcp-01",
      "node_id": 1,
      "role": "PRIMARY",
      "vip_assigned": true,
      "heartbeat_time": "2026-08-29T10:30:00Z",
      "heartbeat_age_ms": 250,
      "is_healthy": true,
      "api_address": "192.168.1.10:8080"
    },
    {
      "server_id": "uuid-2",
      "node_name": "dhcp-02",
      "node_id": 2,
      "role": "SECONDARY",
      "vip_assigned": false,
      "heartbeat_time": "2026-08-29T10:29:58Z",
      "heartbeat_age_ms": 2250,
      "is_healthy": true,
      "api_address": "192.168.1.11:8080"
    }
  ],
  "cluster_state": "HEALTHY",
  "notes": ""
}
```

---

End of endpoints.md
