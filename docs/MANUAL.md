# gdhcp Operator Manual

This manual covers day-to-day operation of gdhcp: deployment, initial setup, configuration, IPAM and DHCP management, RBAC, and troubleshooting. It assumes familiarity with networking fundamentals and Unix system administration.

**Links over copy-paste:** This manual links extensively to deeper docs rather than restating them. Core references are [AMENDMENTS.md](AMENDMENTS.md) (binding contract), [ARCHITECTURE.md](ARCHITECTURE.md) (system design), and [api/endpoints.md](api/endpoints.md) (REST API reference). The design docs in [design/](design/) provide the reasoning; deployment docs in [deploy/](deploy/) cover the three target platforms.

---

## Table of Contents

1. [Installation](#1-installation)
2. [Initial Setup](#2-initial-setup)
3. [First Login & Security](#3-first-login--security)
4. [Web UI Tour](#4-web-ui-tour)
5. [IPAM Management](#5-ipam-management)
6. [DHCP Scopes & Pools](#6-dhcp-scopes--pools)
7. [Lease Management](#7-lease-management)
8. [HA Clustering](#8-ha-clustering)
9. [CLI Reference](#9-cli-reference)
10. [Configuration Reference](#10-configuration-reference)
11. [RBAC & User Management](#11-rbac--user-management)
12. [Day-2 Operations](#12-day-2-operations)
13. [Monitoring & Metrics](#13-monitoring--metrics)
14. [Backup & Recovery](#14-backup--recovery)
15. [Upgrade & Maintenance](#15-upgrade--maintenance)
16. [Troubleshooting](#16-troubleshooting)
17. [Appendices](#17-appendices)

---

## 1. Installation

gdhcp runs on Linux (amd64, arm64) with PostgreSQL 12+. Choose a deployment model:

| Model | Suitable for | Docs |
|-------|-------------|------|
| **Bare-metal** (systemd) | Production HA clusters, brownfield | [deploy/bare-metal.md](deploy/bare-metal.md) |
| **Docker Compose** | Single-node, labs, quick start | [deploy/docker.md](deploy/docker.md) |
| **Kubernetes** | Cloud-native, multi-tenancy | [deploy/kubernetes.md](deploy/kubernetes.md) |

Each deployment guide includes VIP failover setup for HA. Use `scripts/install.sh` for systemd; for Docker and Kubernetes, see their respective guides.

### Quick-start (systemd)

```bash
# 1. Ensure Postgres is running (gdhcp will NOT create the database)
sudo -u postgres createdb gdhcp
sudo -u postgres psql gdhcp -c "CREATE USER gdhcp WITH PASSWORD 'changeme';"
sudo -u postgres psql gdhcp -c "GRANT ALL PRIVILEGES ON DATABASE gdhcp TO gdhcp;"

# 2. Install gdhcp
sudo scripts/install.sh

# 3. Edit config and run migrations
sudo -u gdhcp /usr/local/bin/gdhcp migrate --config-file /etc/gdhcp/config.yaml
sudo systemctl edit gdhcp.service  # Add GDHCP_DATABASE_DSN if needed, or edit /etc/gdhcp/config.yaml
sudo systemctl start gdhcp
sudo systemctl status gdhcp

# 4. Access the web UI at http://localhost:8080
```

Post-install, the gdhcp binary is at `/usr/local/bin/gdhcp`, config at `/etc/gdhcp/config.yaml`, data at `/var/lib/gdhcp`, and logs at `/var/log/gdhcp`.

---

## 2. Initial Setup

### 2.1 Configuration File

gdhcp reads `config.yaml` from `/etc/gdhcp` (systemd) or a path you specify via `--config-file`. A template is at [config.yaml.example](../config.yaml.example). Key sections:

- **`server.api_addr`** — HTTP listener (e.g. `0.0.0.0:8080`)
- **`server.jwt_secret`** — Signs session tokens; leave empty for dev (random secret generated at startup)
- **`database.dsn`** — PostgreSQL connection string
- **`dhcp.interfaces`** — Network adapters to listen on (empty list = API/UI only)
- **`ha.enabled`** — true to join an HA cluster
- **`logging` / `metrics`** — Observability tuning

See [Section 10: Configuration Reference](#10-configuration-reference) for all settings.

### 2.2 Environment Variable Overrides

Any config key can be overridden with `GDHCP_<SECTION>_<FIELD>`. Examples:

```bash
# Override Postgres connection
GDHCP_DATABASE_DSN="postgres://gdhcp:pwd@db.example.com/gdhcp" gdhcp serve

# Enable HA for this node
GDHCP_HA_ENABLED=true GDHCP_HA_SERVER_ID=dhcp-01 gdhcp serve

# Set log level
GDHCP_LOGGING_LEVEL=debug gdhcp serve
```

Precedence: CLI flag > environment variable > config file > built-in default.

### 2.3 Database Schema

Initialize the database with embedded migrations:

```bash
# Standalone
gdhcp migrate --config-file /etc/gdhcp/config.yaml

# Or let the server auto-migrate on startup
gdhcp serve --auto-migrate  # default: true
```

This creates tables for VRFs, prefixes, VLANs, DHCP scopes/pools/leases, users, API tokens, audit logs, and more. See [db/schema.sql](db/schema.sql) for the DDL.

---

## 3. First Login & Security

### 3.1 Default Credentials

**Default admin user:** `admin` / `admin`, seeded by migration `0003_seed_admin.sql`.
Replace it before the API is reachable by anything you do not trust.

There is no password-change endpoint in v0.1.0. `PUT /api/v1/users/{id}` accepts
`username`, `email`, `description`, `is_active` and `role` — deliberately not `password`
(see `server/internal/api/users/handler.go`, `updateBody`). The Admin > Users dialog
collects a password only when *creating* a user, for the same reason.

So replace the account rather than editing it:

```bash
# 1. Create your own admin, logged in as admin/admin.
#    --password is required; pipe it in rather than leaving it in shell history.
gdhcp user create --username ops --email ops@example.com \
  --password "$(openssl rand -base64 18)"

# 2. Log in as the new account.
gdhcp login --server http://localhost:8080

# 3. Deactivate the seeded admin (user ID 1). Deactivating keeps its audit-log
#    rows attributable; `gdhcp user delete 1` would remove the account entirely.
#    --username and --email must be repeated: the API's update handler requires
#    both, and the CLI omits any flag you do not pass. The seeded values are
#    `admin` / `admin@localhost` (migration 0003_seed_admin.sql).
gdhcp user update 1 --username admin --email admin@localhost --active=false
```

Do step 3 from the *new* account — deactivating the account you are signed in as will
lock you out at the next request.

### 3.2 API Tokens

For programmatic access (scripts, monitoring), create an API token:

1. Web UI: Admin > Users > select user > Tokens > Create
2. CLI: `gdhcp user token create <user-id> --description "automation" --expires-in-days 365`

Tokens are shown exactly once; store securely (e.g. in a secrets manager). Use with `gdhcp --token <token>` or `GDHCP_TOKEN=<token>`.

### 3.3 Session Management

Access tokens expire after 24 hours and refresh tokens after 7 days; both are constants in
`server/internal/api/jwtauth.go` (`accessTokenTTL`, `refreshTokenTTL`). Log in again to renew:

```bash
gdhcp login --server http://localhost:8080
# Prompts for username and password, saves session to ~/.gdhcp.yaml
```

---

## 4. Web UI Tour

The gdhcp web UI is embedded in the binary and served at `http://<gdhcp-ip>:8080`. Five main sections:

### 4.1 Dashboard
- **Cluster Health:** Active node, failover status, heartbeat lag (HA only)
- **Lease Statistics:** Active/released/abandoned leases per scope
- **Utilization:** Pool and prefix space usage
- **Events:** Live feed of lease churn and cluster state changes (WebSocket/SSE)

### 4.2 IPAM
- **VRF Browser:** View routing domains and their prefix trees
- **Prefix Hierarchy:** Drill into aggregates, subnets, containers
- **VLAN Management:** Assign VLANs to prefixes, browse VLAN groups
- **Prefix Clients:** Every lease and reservation on a prefix (including descendants)
- **Utilization:** Address-space allocation per prefix (child coverage) and DHCP (lease usage)

**Navigation:** The IPAM tree is on the left; click a prefix to view its detail page, scopes, and DHCP clients.

### 4.3 DHCP
Three tabs:

1. **Scopes** — View/create/edit DHCP scopes (one per subnet). Each scope contains pools.
2. **Pools** — View/create/edit allocation ranges within a scope. Per-pool:
   - **Leases tab** — All active/released/abandoned leases in this pool
   - **Reservations tab** — Static IP assignments (MAC/client-ID/hostname/etc.)
   - **Options tab** — DHCP options to offer clients in this pool
3. **Leases** — Global lease view with search and bulk actions:
   - **Pin** — Reserve an IP for a lease's client (creates a reservation)
   - **Renew** — Force-reissue a lease (useful for testing)
   - **Release** — Return an IP to the pool
   - **Abandon** — Hold back from reallocation (e.g., if the address is answering on the wire)
   - **Delete** — Remove the lease record

**Lease States:**
- `binding_sent` — DHCPOFFER sent, awaiting REQUEST
- `active` — Lease is current (client can use the IP)
- `released` — Client released it or lease expired; IP is free
- `abandoned` — Held back; will be freed by the reaper after retention period

Also under DHCP: **Templates** (named DHCP option sets) and **Options** (the IANA catalog).

### 4.4 HA Cluster
- **Cluster Status:** List all nodes in this HA cluster, their roles (primary/secondary), heartbeat age, VIP assignment
- **Manual Failover:** Force a secondary to become primary (if the primary is down)

### 4.5 Admin
- **Users:** Create/update/delete users, change roles (admin/operator/viewer), disable accounts
- **API Tokens:** Create/revoke tokens for each user, set expiration
- **Audit Log:** Every CRUD operation (what, who, when, entity)
- **Server Health:** API version, database status, uptime

---

## 5. IPAM Management

### 5.1 VRFs
A VRF (Virtual Routing & Forwarding domain) isolates routing tables. Each DHCP scope lives in exactly one VRF. For single-router deployments, use the `default` VRF.

**Create via Web UI:** IPAM > (from prefix detail) or Admin > (global settings)  
**Create via CLI:**
```bash
gdhcp vrf create --name default --description "Main routing domain"
gdhcp vrf list
gdhcp vrf get <vrf-id>
```

### 5.2 Prefixes & IPAM Hierarchy
Prefixes are CIDR blocks. Hierarchy:
- **Aggregate** (e.g., `10.0.0.0/8`) — top-level routing block
- **Container** (e.g., `10.1.0.0/16`) — sub-aggregate, no DHCP
- **Subnet** (e.g., `10.1.1.0/24`) — DHCP scope lives here

When you create a scope, it auto-creates a subnet prefix if one doesn't exist.

**Create via CLI:**
```bash
# Aggregate
gdhcp prefix create --vrf-id default --prefix 10.0.0.0/8 --prefix-type aggregate

# Subnet (reserved for DHCP)
gdhcp prefix create --vrf-id default --prefix 10.1.1.0/24 --prefix-type subnet

# Container (for organization, no DHCP)
gdhcp prefix create --vrf-id default --prefix 10.1.0.0/16 --parent-id <aggregate-id> --prefix-type container
```

**Utilization:** Each prefix shows two views:
- **DHCP utilization** (if a scope exists): leases/reserved IPs vs. pool size
- **Address-space utilization:** how much of the prefix's CIDR is covered by child prefixes

### 5.3 VLANs
A VLAN (Virtual LAN) assigns layer-2 tags to subnets. Optional; useful for larger deployments.

**Create via CLI:**
```bash
# VLAN group (optional)
gdhcp vlan group create --name campus --min-vid 100 --max-vid 200

# VLAN
gdhcp vlan create --vid 101 --name "guest-wifi" --group-id <group-id> --status active

# Assign to a prefix
gdhcp prefix update <prefix-id> --vlan-id <vlan-id>
```

**View:**
```bash
gdhcp vlan list [--group-id <group-id>]
gdhcp vlan get <vlan-id>
# Get all prefixes in a VLAN
gdhcp vlan prefixes <vlan-id>
```

### 5.4 Tags
Label prefixes, VLANs, and leases for organization (e.g., `env:prod`, `team:network`). Tags are free-form and queryable.

**Create via CLI:**
```bash
# Tags are created implicitly; just use them
gdhcp prefix update <prefix-id> --tags "env:prod,tier:core"

# Search for tagged entities
gdhcp search --q "tag:env:prod"
```

---

## 6. DHCP Scopes & Pools

### 6.1 Scopes
A scope is a subnet boundary — one DHCP scope per subnet. Scopes contain pools (allocation ranges) and inherit options from templates.

**Create via Web UI:**  
DHCP > Scopes > Create Scope — fills in subnet prefix and VRF from your choice.

**Create via CLI:**
```bash
gdhcp scope create \
  --vrf-id default \
  --network 10.1.1.0/24 \
  --name "Office LAN" \
  --description "Main office subnet"
```

### 6.2 Pools
Pools are contiguous IP ranges within a scope. A scope can have multiple pools (for prioritization, exclusions, or testing).

**Create a pool:**
```bash
gdhcp pool create \
  --scope-id <scope-id> \
  --range-start 10.1.1.100 \
  --range-end 10.1.1.200 \
  --name "Workstations" \
  --priority 10
```

**Pool priority** (0 = lowest): When a client requests an IP, gdhcp tries pools in order of decreasing priority. Useful for reserving one pool for servers (priority 100) and another for guests (priority 1).

**Pool utilization:**
```bash
gdhcp pool utilization <pool-id>
# Shows: total IPs, active leases, reserved IPs, available, utilization %
```

### 6.3 DHCP Options
Clients receive options (DNS, NTP, routers, domain name, etc.) via DHCP Option 55 (DHCP Parameter Request List). Gdhcp has three places to set options:

1. **Template** (scope-wide defaults) — applied to every client in the scope
2. **Pool options** — override scope defaults for a specific pool
3. **Reservations** — override pool options for a specific MAC/client-ID

**Assign a template to a scope:**
```bash
# Use the built-in template
gdhcp scope update <scope-id> --dhcp-template-id <template-id>

# Or create a custom template
gdhcp template create --name "Office Defaults" \
  --description "DNS, NTP, routers for office"
# Then add options via the web UI or API
```

**Set pool-level options:**
```bash
# Get available options
gdhcp option list [--code 6]  # 6 = DNS servers

# Assign to pool
gdhcp pool option set <pool-id> --option-code 6 --value "8.8.8.8 8.8.4.4"
```

See [Appendix: DHCP Options](#appendix-dhcp-options) for a list of common codes and their value types.

### 6.4 Reservations
Bind a specific IP to a client based on MAC address, client identifier, hostname, relay circuit ID, vendor class, or other identifiers. Useful for servers, printers, IoT devices.

**Create a reservation:**
```bash
# Via CLI
gdhcp pool reservation create <pool-id> \
  --binding-type mac \
  --binding-value aa:bb:cc:dd:ee:ff \
  --ip-address 10.1.1.50 \
  --description "Printer hq-1"

# Via Web UI: DHCP > Pools > (select pool) > Reservations > Add
```

**Binding types** (see [AMENDMENTS.md A17.2](AMENDMENTS.md#a172-seven-binding-types)):

| Type | Value Example | Match Priority |
|------|------|----------|
| `client_id` | `01aabbccddee` (DHCP option 61, hex) | 1st — most specific |
| `mac` | `aa:bb:cc:dd:ee:ff` (hardware address) | 2nd |
| `circuit_id` | `eth 1/0/24` or hex (option 82.1) | 3rd |
| `remote_id` | hex (option 82.2) | 4th |
| `vendor_class` | hex or text (option 60) | 5th |
| `user_class` | hex or text (option 77) | 6th |
| `hostname` | `printer-hq-1` (option 12) | 7th — least specific |

When a client matches multiple reservations, the highest-priority binding type wins.

---

## 7. Lease Management

### 7.1 Lease Lifecycle

1. **DISCOVER:** Client broadcasts; server offers an IP
2. **REQUEST:** Client requests that IP; server acknowledges (DHCPACK)
3. **RENEW (T1):** Client renews the lease (default: every 12 hours)
4. **REBIND (T2):** If RENEW fails, rebind (default: every 21 hours)
5. **EXPIRE:** Lease expires (default: 24 hours)

Gdhcp stores every offer and acknowledgement as a lease record with `binding_state`:
- `binding_sent` — DHCPOFFER sent; awaiting REQUEST
- `active` — DHCPACK sent; client can use the IP
- `released` — Client sent DHCPRELEASE or lease expired
- `abandoned` — Held back (address is answering on wire or reserved)

### 7.2 Viewing Leases

**Web UI:** DHCP > Leases (global view, searchable) or drill from a pool's Leases tab

**CLI:**
```bash
# List all leases
gdhcp lease list

# Filter by scope/pool/state
gdhcp lease list --scope-id <id> --pool-id <id> --state active

# Get a single lease
gdhcp lease get <lease-id>

# Find by IP
gdhcp lease by-ip 10.1.1.50

# Find by MAC
gdhcp lease by-mac aa:bb:cc:dd:ee:ff
```

### 7.3 Lease Actions

**Release** — Return an IP to the pool (state → `released`)
```bash
gdhcp lease release <lease-id>
gdhcp lease release <id1> <id2> <id3>  # Bulk
```

**Abandon** — Hold back from reallocation (state → `abandoned`, e.g., for addresses already in use on the wire)
```bash
gdhcp lease abandon <lease-id>
```

**Renew** — Force-issue the lease again (extend expiry or reactivate an expired/released lease)
```bash
gdhcp lease renew <lease-id>
gdhcp lease renew <lease-id> --duration 86400  # 24 hours
```

**Delete** — Remove the lease record outright
```bash
gdhcp lease delete <lease-id>
```

**Bulk actions** (up to 500 leases per request):
```bash
# Release many leases at once (returns per-lease outcomes)
gdhcp lease release <id1> <id2> <id3> ... <id500>
```

### 7.4 Lease Retention
Expired and released leases are kept for `database.lease_retention_days` (default: 90). After that, rows are purged by an internal reaper. To keep leases longer, increase this setting:

```yaml
database:
  lease_retention_days: 180  # 6 months
```

---

## 8. HA Clustering

### 8.1 Architecture
An HA cluster spans multiple gdhcp nodes. One is **Primary** (serves DHCP, owns the VIP); others are **Secondary** (ready to take over). All nodes share one Postgres database and heartbeat each other every ~1 second. If the Primary doesn't heartbeat for 3 seconds (configurable), a Secondary wins an election and becomes Primary.

See [design/ha-cluster.md](design/ha-cluster.md) for the detailed model.

### 8.2 Setup (Bare-Metal)
Follow [deploy/bare-metal.md](deploy/bare-metal.md) for the full walkthrough. At a high level:

1. Install gdhcp on all nodes (same binary, same Postgres connection string)
2. Configure each node's identity (node_id, server_id, server_name)
3. Set a shared VIP (virtual IP) and the interface to hold it
4. Tune heartbeat timings
5. Start all nodes; they will elect a Primary and move the VIP

**Example config for a 2-node cluster:**

Node 1:
```yaml
ha:
  enabled: true
  cluster_id: "prod-cluster"
  server_id: "dhcp-01"
  server_name: "dhcp-server-1"
  node_id: 1          # Lower node_id wins elections
  vip_address: "10.0.0.10/24"
  vip_interface: "eth0"
  heartbeat_interval_ms: 1000
  heartbeat_ttl_ms: 3000     # >= 2 * interval
  election_interval_ms: 2000 # <= ttl / 2
```

Node 2:
```yaml
ha:
  cluster_id: "prod-cluster"
  server_id: "dhcp-02"
  server_name: "dhcp-server-2"
  node_id: 2          # Higher node_id, so Node 1 always wins
  vip_address: "10.0.0.10/24"
  vip_interface: "eth0"
  heartbeat_interval_ms: 1000
  heartbeat_ttl_ms: 3000
  election_interval_ms: 2000
```

### 8.3 Monitoring Failover
**Web UI:** HA > Cluster Status — shows the current Primary, Secondary roles, and heartbeat lag

**CLI:**
```bash
gdhcp ha status
# Output: cluster_id, primary_node_id, election_epoch, vip_address, all_nodes []

gdhcp cluster list
# Shows all nodes, their roles, last_heartbeat_at, is_primary
```

### 8.4 Manual Failover
If the Primary is hung or slow:

```bash
gdhcp ha failover --to <node-id>
# Forces a Secondary to assume Primary role; returns 409 if the current Primary has just heartbeat
```

---

## 9. CLI Reference

All gdhcp CLI subcommands talk to a running gdhcp server via REST API. Set `--server` and `--token` (or use `gdhcp login` to save them).

### 9.1 Server Lifecycle

**`gdhcp serve`** — Run the DHCP server, API, and web UI
```bash
gdhcp serve [--config-file PATH] [--auto-migrate]
```

**`gdhcp migrate`** — Run database migrations and exit
```bash
gdhcp migrate [--config-file PATH]
```

**`gdhcp version`** — Print the build version
```bash
gdhcp version
```

### 9.2 Authentication

**`gdhcp login`** — Authenticate and save a session token
```bash
gdhcp login --server http://localhost:8080 [--username USER] [--password PASS]
# Writes ~/.gdhcp.yaml with server and token; later commands use it automatically
```

**Global flags** (available on all API-client commands):
- `--server URL` — API server address (default: `http://localhost:8080`, or `GDHCP_SERVER`)
- `--token TOKEN` — Bearer token (or `GDHCP_TOKEN`)
- `--output {table|json|yaml}` — Output format (default: `table`)
- `--config PATH` — Client config file (default: `~/.gdhcp.yaml`)

### 9.3 IPAM Commands

**`gdhcp vrf`** — Manage Virtual Routing Domains
```bash
gdhcp vrf list                        # List all VRFs
gdhcp vrf get <vrf-id>               # Describe one VRF
gdhcp vrf create --name NAME [--description DESC]
gdhcp vrf update <vrf-id> [--name NAME] [--description DESC]
gdhcp vrf delete <vrf-id>
```

**`gdhcp prefix`** — Manage prefixes (aggregates, subnets, containers)
```bash
gdhcp prefix list --vrf-id VRF_ID [--prefix-type subnet|aggregate|container]
gdhcp prefix get <prefix-id>
gdhcp prefix create --vrf-id VRF_ID --prefix CIDR --prefix-type TYPE \
  [--parent-id PARENT] [--description DESC]
gdhcp prefix update <prefix-id> [--name NAME] [--tags "tag1,tag2"] [--vlan-id VLAN_ID]
gdhcp prefix delete <prefix-id>
gdhcp prefix utilization <prefix-id>               # Shows DHCP + space utilization
gdhcp prefix ancestors <prefix-id>                 # Root-first path to this prefix
gdhcp prefix clients <prefix-id>                   # Leases and reservations on this prefix
```

**`gdhcp vlan`** — Manage VLANs
```bash
gdhcp vlan list [--group-id GROUP_ID]
gdhcp vlan get <vlan-id>
gdhcp vlan create --vid VID --name NAME [--group-id GROUP] [--status active|reserved|deprecated]
gdhcp vlan update <vlan-id> [--name NAME] [--status STATUS]
gdhcp vlan delete <vlan-id>
gdhcp vlan group list                  # List VLAN groups
gdhcp vlan group create --name NAME [--min-vid 100] [--max-vid 200]
gdhcp vlan group delete <group-id>
gdhcp vlan prefixes <vlan-id>         # Prefixes assigned to this VLAN
```

**`gdhcp tag`** — Manage tags (free-form labels)
```bash
gdhcp tag list [--entity-type TYPE]    # TYPE: prefix, vlan, lease
gdhcp tag get <tag-id>
gdhcp tag delete <tag-id>
```

**`gdhcp tree`** — View the prefix hierarchy
```bash
gdhcp tree [--vrf-id VRF_ID]           # ASCII tree view
```

### 9.4 DHCP Commands

**`gdhcp scope`** — Manage DHCP scopes
```bash
gdhcp scope list [--vrf-id VRF_ID]
gdhcp scope get <scope-id>
gdhcp scope create --vrf-id VRF_ID --network CIDR --name NAME [--description DESC]
gdhcp scope update <scope-id> [--name NAME] [--dhcp-template-id TEMPLATE]
gdhcp scope delete <scope-id>
gdhcp scope utilization <scope-id>     # Shows lease usage
```

**`gdhcp pool`** — Manage pools (IP ranges within scopes)
```bash
gdhcp pool list --scope-id SCOPE_ID
gdhcp pool get <pool-id>
gdhcp pool create --scope-id SCOPE_ID --range-start START --range-end END \
  --name NAME [--priority 10] [--description DESC]
gdhcp pool update <pool-id> [--priority 10] [--enabled false]
gdhcp pool delete <pool-id>
gdhcp pool utilization <pool-id>       # Shows allocation breakdown
gdhcp pool reservation <subcommand>    # See section 9.4.1 below
gdhcp pool option <subcommand>         # See section 9.4.2 below
gdhcp pool leases <pool-id> [filters]  # List leases in pool
```

**9.4.1 Reservations (under `pool reservation`)**
```bash
gdhcp pool reservation list <pool-id>
gdhcp pool reservation get <reservation-id>
gdhcp pool reservation create <pool-id> --binding-type TYPE --binding-value VALUE \
  --ip-address IP [--description DESC]
  # TYPE: mac, client_id, circuit_id, remote_id, vendor_class, user_class, hostname
gdhcp pool reservation update <reservation-id> [--enabled false]
gdhcp pool reservation delete <reservation-id>
```

**9.4.2 Pool Options (under `pool option`)**
```bash
gdhcp pool option list <pool-id>
gdhcp pool option set <pool-id> --option-code CODE --value "value1 value2"
gdhcp pool option delete <pool-id> --option-code CODE
```

**`gdhcp lease`** — Manage DHCP leases
```bash
gdhcp lease list [--scope-id S] [--pool-id P] [--state active|released|abandoned] [--search TERM]
gdhcp lease get <lease-id>
gdhcp lease by-ip 10.1.1.50
gdhcp lease by-mac aa:bb:cc:dd:ee:ff
gdhcp lease release <lease-id> [<id2> ...]        # Bulk: return IPs to pool
gdhcp lease abandon <lease-id> [<id2> ...]        # Bulk: hold back from allocation
gdhcp lease renew <lease-id> [<id2> ...] [--duration SEC]  # Bulk: force-renew
gdhcp lease delete <lease-id> [<id2> ...]         # Bulk: remove records
```

**`gdhcp template`** — Manage DHCP option templates
```bash
gdhcp template list
gdhcp template get <template-id>
gdhcp template create --name NAME [--description DESC]
gdhcp template update <template-id> [--name NAME]
gdhcp template delete <template-id>
gdhcp template option list <template-id>
gdhcp template option set <template-id> --option-code CODE --value "..."
gdhcp template option delete <template-id> --option-code CODE
```

**`gdhcp option`** — Query the IANA DHCP option catalog
```bash
gdhcp option list [--code CODE]        # Browse all options or one by code
gdhcp option get <option-code>         # Describe one option (code, type, RFC)
```

### 9.5 HA Commands

**`gdhcp cluster`** — Cluster membership
```bash
gdhcp cluster list
gdhcp cluster get <cluster-id>
```

**`gdhcp ha`** — Cluster role and failover
```bash
gdhcp ha status                        # Current Primary, election epoch, all nodes
gdhcp ha failover --to <node-id>       # Force Secondary to Primary
```

### 9.6 Admin Commands

**`gdhcp user`** — User management (admin only)
```bash
gdhcp user list
gdhcp user get <user-id>
gdhcp user create --username USER --email EMAIL --password PASS [--description DESC]
gdhcp user update <user-id> [--username U] [--email E] [--active false]
gdhcp user delete <user-id>
```

**`gdhcp user token`** — API token management
```bash
gdhcp user token list <user-id>
gdhcp user token create <user-id> --description DESC [--expires-in-days 365]
gdhcp user token revoke <user-id> <token-id>
```

### 9.7 Search

**`gdhcp search`** — Global search across all entities
```bash
gdhcp search --q "10.1.1"              # Find prefixes, pools, leases with that IP/text
gdhcp search --q "tag:env:prod"        # Tagged entities
gdhcp search --q "printer-hq-1"        # Free-text search
```

Results are ranked and include entity type + URL to web UI.

### 9.8 Output Formats

All list commands support `--output` to change format:

```bash
gdhcp scope list --output table        # Human-readable (default)
gdhcp scope list --output json         # JSON objects
gdhcp scope list --output yaml         # YAML
```

Add `--limit 100 --offset 20` to paginate.

---

## 10. Configuration Reference

### 10.1 Config File Structure

```yaml
server:
  api_addr: "0.0.0.0:8080"             # HTTP listener address
  api_tls_enabled: false               # Enable HTTPS
  api_tls_cert: "/path/to/cert.pem"   # TLS certificate (if api_tls_enabled: true)
  api_tls_key: "/path/to/key.pem"     # TLS private key (if api_tls_enabled: true)
  jwt_secret: ""                       # Session token signing key; leave empty to auto-generate
  cors_allowed_origins: []             # Origins allowed for credentialed CORS requests

database:
  dsn: "postgres://user:pass@host/db?sslmode=disable"  # PostgreSQL connection string
  max_open_conns: 25                   # Max database connection pool size
  max_idle_conns: 5                    # Idle connections to keep alive
  conn_max_lifetime: "5m"              # Max lifetime of a connection (duration format)
  lease_retention_days: 90             # Expired/released leases kept this long
  option_cache_ttl: "60s"              # Cache IANA option catalog this long (duration)

dhcp:
  interfaces: []                       # Network adapters to listen on (empty = API/UI only)
  default_lease_time: 86400            # Lease duration in seconds (24h)
  default_renewal_time: 43200          # T1 (RENEW) at 50% of lease time (12h)
  default_rebind_time: 75600           # T2 (REBIND) at 87.5% of lease time (21h)

ha:
  enabled: false                       # Enable HA clustering
  cluster_id: "prod-cluster"           # Cluster name (shared across all nodes)
  server_id: "dhcp-01"                 # This node's unique ID in the cluster
  server_name: "dhcp-server-1"         # Human-readable node name
  node_id: 1                           # Unique integer per node; lower wins elections
  vip_address: "10.0.0.10/24"         # Virtual IP for clients to use (w/ CIDR)
  vip_interface: "eth0"                # Interface to hold the VIP
  heartbeat_interval_ms: 1000          # How often nodes heartbeat (milliseconds)
  heartbeat_ttl_ms: 3000               # Heartbeat considered stale after this long (>= 2 * interval)
  election_interval_ms: 2000           # Election check interval (<= ttl / 2)
  config_check_interval_ms: 5000       # Check for config changes this often
  config_listen_enabled: true          # Accept cluster members' config updates (reload endpoint)

logging:
  level: "info"                        # Log level: debug, info, warn, error
  format: "json"                       # Log format: json or text
  output: "stdout"                     # Log destination: stdout or filepath

metrics:
  enabled: true                        # Enable Prometheus metrics
  listen_addr: "0.0.0.0:9090"         # Metrics listener address
  path: "/metrics"                     # Metrics HTTP path
```

### 10.2 Environment Variable Overrides

Any config key can be set via `GDHCP_<SECTION>_<FIELD>`:

| Config Path | Environment Variable | Example Value |
|---|---|---|
| `server.api_addr` | `GDHCP_SERVER_API_ADDR` | `0.0.0.0:8080` |
| `server.api_tls_enabled` | `GDHCP_SERVER_API_TLS_ENABLED` | `true` or `false` |
| `server.jwt_secret` | `GDHCP_SERVER_JWT_SECRET` | `my-secret-key` |
| `server.cors_allowed_origins` | `GDHCP_SERVER_CORS_ALLOWED_ORIGINS` | `http://localhost:4200,https://example.com` (comma-separated) |
| `database.dsn` | `GDHCP_DATABASE_DSN` | `postgres://user:pass@host/db` |
| `database.max_open_conns` | `GDHCP_DATABASE_MAX_OPEN_CONNS` | `25` (integer) |
| `database.lease_retention_days` | `GDHCP_DATABASE_LEASE_RETENTION_DAYS` | `90` |
| `database.option_cache_ttl` | `GDHCP_DATABASE_OPTION_CACHE_TTL` | `60s` (duration) |
| `dhcp.default_lease_time` | `GDHCP_DHCP_DEFAULT_LEASE_TIME` | `86400` |
| `dhcp.interfaces` | `GDHCP_DHCP_INTERFACES` | `eth0:default,eth1:mgmt` (name:vrf_id pairs) |
| `ha.enabled` | `GDHCP_HA_ENABLED` | `true` or `false` |
| `ha.server_id` | `GDHCP_HA_SERVER_ID` | `dhcp-01` |
| `ha.cluster_id` | `GDHCP_HA_CLUSTER_ID` | `prod-cluster` |
| `ha.node_id` | `GDHCP_HA_NODE_ID` | `1` |
| `ha.vip_address` | `GDHCP_HA_VIP_ADDRESS` | `10.0.0.10/24` |
| `ha.vip_interface` | `GDHCP_HA_VIP_INTERFACE` | `eth0` |
| `ha.heartbeat_interval_ms` | `GDHCP_HA_HEARTBEAT_INTERVAL_MS` | `1000` |
| `logging.level` | `GDHCP_LOGGING_LEVEL` | `debug`, `info`, `warn`, `error` |
| `logging.format` | `GDHCP_LOGGING_FORMAT` | `json` or `text` |
| `metrics.enabled` | `GDHCP_METRICS_ENABLED` | `true` or `false` |
| `metrics.listen_addr` | `GDHCP_METRICS_LISTEN_ADDR` | `0.0.0.0:9090` |

Example:
```bash
GDHCP_DATABASE_DSN="postgres://user:pass@db.example.com/gdhcp" \
GDHCP_HA_ENABLED=true \
GDHCP_HA_SERVER_ID=dhcp-01 \
gdhcp serve
```

### 10.3 Type Conversions

- **bool:** `true`, `false`, `yes`, `no`, `1`, `0` (case-insensitive)
- **int:** Decimal number (e.g., `25`, `1000`)
- **duration:** Go duration format (e.g., `5m`, `30s`, `2h30m`)
- **list:** Comma-separated (e.g., `eth0:default,eth1:mgmt` for interfaces)

---

## 11. RBAC & User Management

gdhcp has three roles:

| Role | Permissions |
|------|---|
| **admin** | Full access: CRUD on all resources, user management, cluster config, audit log |
| **operator** | CRUD on IPAM/DHCP resources (prefixes, scopes, leases, etc.), but cannot manage users, tokens, or cluster |
| **viewer** | Read-only access to all resources |

API tokens inherit their user's role.

### 11.1 Default Credentials

**⚠️ Always change these immediately:**

```bash
# Web UI: http://localhost:8080
# Username: admin
# Password: admin

# OR via CLI
gdhcp user update 1 --password $(openssl rand -base64 12)
```

### 11.2 Creating Users

**Web UI:** Admin > Users > Create User  
**CLI:**
```bash
gdhcp user create --username alice --email alice@example.com --password $(openssl rand -base64 12)
```

New users default to `admin` role. To set a different role (requires updating the DB directly in current version; role selection is in the web UI):

```sql
UPDATE users SET role = 'operator' WHERE username = 'alice';
```

### 11.3 API Tokens

Users can create API tokens for programmatic access. Tokens inherit the user's role.

**Create a token:**
```bash
gdhcp user token create <user-id> --description "Jenkins CI" --expires-in-days 365
# Token is shown exactly once; store it securely
```

**Use a token:**
```bash
gdhcp --server http://localhost:8080 --token gdhcp_<token> scope list

# Or set environment variable
GDHCP_TOKEN=gdhcp_<token> gdhcp scope list
```

**Revoke a token:**
```bash
gdhcp user token revoke <user-id> <token-id>
```

### 11.4 Audit Log

Every CRUD operation is logged. View in:  
**Web UI:** Admin > Audit Log  
**API:** `GET /api/v1/audit-log`

Log entry includes: who, what action, which resource, when, and status (success/failure).

---

## 12. Day-2 Operations

### 12.1 Monitoring & Alerts

See [operations/monitoring.md](operations/monitoring.md) for Prometheus metrics and recommended alert rules. Key metrics:

- `gdhcp_cluster_primary` — Is this node primary? (1 = yes)
- `gdhcp_cluster_heartbeat_ms` — Heartbeat lag
- `gdhcp_pool_active_leases` — Active leases per pool
- `gdhcp_pool_capacity_used` — Pool utilization %
- `gdhcp_dhcp_requests` — DHCP requests/sec
- `gdhcp_dhcp_nak` — NAKs sent (no available IPs)

Enable metrics:
```yaml
metrics:
  enabled: true
  listen_addr: "0.0.0.0:9090"
  path: "/metrics"
```

Then scrape with Prometheus:
```yaml
scrape_configs:
  - job_name: gdhcp
    static_configs:
      - targets: ['localhost:9090']
```

### 12.2 Backup & Recovery

See [operations/backup-recovery.md](operations/backup-recovery.md). In short:

- **State:** All state lives in Postgres; local files are transient (HA heartbeat state, caches)
- **Backup:** Daily `pg_dump` or WAL archive
- **Recovery:** Restore database, start gdhcp; it auto-migrates

Example:
```bash
# Backup
pg_dump gdhcp > backup-$(date +%Y-%m-%d).sql

# Restore
createdb gdhcp-restored
psql gdhcp-restored < backup-2024-01-15.sql
# Update DATABASE_DSN to point to gdhcp-restored, start gdhcp

# Verify
gdhcp scope list --server http://localhost:8080
```

### 12.3 Log Rotation

Logs go to stdout (Docker/Kubernetes) or `/var/log/gdhcp` (systemd). Use `logrotate` for systemd:

```bash
# /etc/logrotate.d/gdhcp
/var/log/gdhcp/*.log {
  daily
  rotate 7
  compress
  delaycompress
  notifempty
  create 0640 gdhcp gdhcp
  sharedscripts
  postrotate
    systemctl reload gdhcp > /dev/null 2>&1 || true
  endscript
}
```

### 12.4 Health Checks

**Web UI:** Admin > Server Health — API version, database status, uptime  
**CLI:**
```bash
gdhcp ha status                       # HA cluster health
curl http://localhost:8080/api/v1/health  # JSON endpoint
```

### 12.5 Configuration Reload (HA Cluster)

If you change the config file and want to propagate it to all cluster members without restarting:

```bash
# On each node, reload config (requires config_listen_enabled: true in ha section)
curl -X POST http://localhost:8080/api/v1/cluster/config-reload

# Or
gdhcp ha reload-config  # (if this command exists)
```

Check logs to confirm all nodes picked up the change:
```bash
tail -f /var/log/gdhcp/*.log | grep config
```

### 12.6 DHCP Interface Changes

If you add/remove a network interface, you must restart gdhcp:
```bash
systemctl restart gdhcp
```

Changes to `dhcp.interfaces` take effect on next startup only.

### 12.7 Database Maintenance

Periodically vacuum and analyze:
```bash
# As the gdhcp database user
psql gdhcp -c "VACUUM ANALYZE;"
```

Or set up autovacuum in Postgres (usually enabled by default). Monitor vacuum lag:
```bash
psql gdhcp -c "SELECT schemaname, tablename, last_vacuum, last_autovacuum FROM pg_stat_user_tables ORDER BY last_autovacuum DESC LIMIT 10;"
```

---

## 13. Monitoring & Metrics

Refer to [operations/monitoring.md](operations/monitoring.md) for the complete metrics reference. Key sections:

- **Cluster Health:** Primary role, election epochs, heartbeat lag
- **Lease Churn:** DHCP requests, ACKs, NAKs, renewals per second
- **Pool Utilization:** Active leases, capacity, fragmentation
- **API:** Request latency, error rates
- **Database:** Connection pool, query lag, transaction duration

---

## 14. Backup & Recovery

Refer to [operations/backup-recovery.md](operations/backup-recovery.md). Short version:

1. **What to backup:** The Postgres database only; everything else (VIP state, caches) can be rebuilt.
2. **How to backup:** `pg_dump` or WAL archive
3. **How to recover:** Restore the database, start gdhcp (it auto-migrates)

Store backups off-site and test recovery regularly.

---

## 15. Upgrade & Maintenance

### 15.1 Upgrading gdhcp

Check the release notes for breaking changes. The binary is fully backward-compatible with prior databases; migrations run automatically on startup (`--auto-migrate` is the default).

**For a single-node deployment:**
```bash
# 1. Download new binary
wget https://github.com/shamalawy/gdhcp/releases/download/v0.2.0/gdhcp-linux-amd64
chmod +x gdhcp-linux-amd64

# 2. Stop gdhcp
systemctl stop gdhcp

# 3. Backup the database
pg_dump gdhcp > backup-pre-upgrade.sql

# 4. Replace the binary
sudo mv gdhcp-linux-amd64 /usr/local/bin/gdhcp

# 5. Start (auto-migrate)
systemctl start gdhcp
systemctl status gdhcp

# 6. Verify
gdhcp version
gdhcp scope list  # Sanity check
```

**For an HA cluster:**
```
Upgrade one node at a time (rolling update):
1. Stop Node 2 (Secondary)
2. Upgrade Node 2, verify it comes back up
3. Wait for Node 1 to become Primary (should happen automatically if it isn't already)
4. Stop Node 1
5. Upgrade Node 1, verify it comes back up
6. One node will re-elect as Primary (likely Node 1 if node_id is lower)
```

### 15.2 Database Schema Changes

Migrations are embedded in the binary. When you upgrade, the server auto-applies pending migrations on startup. To manually run migrations:

```bash
gdhcp migrate --config-file /etc/gdhcp/config.yaml
```

### 15.3 Deprecations & Breaking Changes

Check [AMENDMENTS.md](AMENDMENTS.md) and the release notes. Breaking changes (if any) are clearly marked.

---

## 16. Troubleshooting

Refer to [deploy/troubleshooting.md](deploy/troubleshooting.md) for deployment-layer issues. Common issues:

### 16.1 "No DHCP listeners started" but `dhcp.interfaces` is configured

Check that the interface names are correct and the network is up:

```bash
# Check system interfaces
ip link show

# Check gdhcp logs
journalctl -u gdhcp -f

# Restart
systemctl restart gdhcp
systemctl status gdhcp
```

### 16.2 VIP Not Moving in HA Cluster

Check:
1. Are all nodes heartbeating? (HA > Cluster Status in web UI)
2. Can you ping the VIP from a client machine?
3. Are there firewall rules blocking the heartbeat port? (Usually UDP 67, but check logs for the actual port)
4. Is the VIP address already in use by another device?

See [deploy/troubleshooting.md](deploy/troubleshooting.md) for more.

### 16.3 Database Connection Errors

```
Error: could not connect to server: Connection refused
```

Check:
1. Is Postgres running? `systemctl status postgresql`
2. Is the DSN correct in config.yaml? (`gdhcp_host`, username, password)
3. Can you connect manually? `psql -h localhost -U gdhcp -d gdhcp`

### 16.4 "No available leases" (DHCP NAK)

If pools are configured but clients get NAK:

1. Check pool utilization (DHCP > Pools, or `gdhcp pool utilization <id>`)
2. Check for abandoned leases (state = `abandoned`); release or delete them
3. Check for stale leases (expired but not reaped yet); adjust `database.lease_retention_days`
4. Check pool priority; make sure pools are enabled (`gdhcp pool list`)

### 16.5 Web UI Won't Load

```
GET http://localhost:8080 → 404 Not Found
```

Check:
1. Is gdhcp running? `systemctl status gdhcp` or `curl http://localhost:8080/api/v1/health`
2. Is the embedded web asset missing? This should not happen in a release build.
3. Check logs: `journalctl -u gdhcp -f`

### 16.6 API Returns 401 Unauthorized

Token is invalid or expired. Re-authenticate:

```bash
gdhcp login --server http://localhost:8080
# Or
GDHCP_TOKEN="" gdhcp --server ... scope list  # Will fail with 401, prompting to login
```

---

## 17. Appendices

### Appendix: DHCP Options

Common DHCP options (RFC 2132). The full IANA catalog is in gdhcp's database (seeded during migration 0002).

| Code | Name | Type | Example |
|------|------|------|---------|
| 1 | Subnet Mask | ip | `255.255.255.0` |
| 3 | Router (default gateway) | ip_list | `10.1.1.1 10.1.1.2` |
| 6 | DNS Servers | ip_list | `8.8.8.8 8.8.4.4` |
| 15 | Domain Name | string | `example.com` |
| 44 | NetBIOS over TCP/IP Name Server | ip_list | `10.1.1.100` |
| 119 | Domain Search | fqdn | `example.com search.example.com` |
| 121 | Classless Static Routes | route_list | `10.2.0.0/16 10.1.1.1` |

To set an option on a pool:

```bash
gdhcp pool option set <pool-id> --option-code 6 --value "8.8.8.8 8.8.4.4"
```

Run `gdhcp option list` (see [section 9, CLI reference](#9-cli-reference)) to see all supported codes and types.

### Appendix: Environment Variables Summary

**Required (unless using config.yaml):**
- `GDHCP_DATABASE_DSN` — PostgreSQL connection string

**Common overrides:**
- `GDHCP_SERVER_API_ADDR` — HTTP listener (default: `0.0.0.0:8080`)
- `GDHCP_SERVER_JWT_SECRET` — Session token signing key
- `GDHCP_LOGGING_LEVEL` — Log level (default: `info`)
- `GDHCP_DHCP_INTERFACES` — Network adapters (e.g., `eth0:default,eth1:mgmt`)
- `GDHCP_HA_ENABLED` — Enable HA clustering (default: `false`)
- `GDHCP_HA_SERVER_ID`, `GDHCP_HA_CLUSTER_ID`, `GDHCP_HA_NODE_ID` — HA identity

**Client CLI:**
- `GDHCP_SERVER` — API server URL (default: `http://localhost:8080`)
- `GDHCP_TOKEN` — Bearer token for authentication

See [Section 10.2](#102-environment-variable-overrides) for the full table.

### Appendix: Systemd Service File

Installed to `/etc/systemd/system/gdhcp.service` by `scripts/install.sh`:

```ini
[Unit]
Description=gdhcp — HA DHCP Server and IPAM
Documentation=https://github.com/shamalawy/gdhcp
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
User=gdhcp
Group=gdhcp
ExecStart=/usr/local/bin/gdhcp serve --auto-migrate
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=gdhcp

# Security/capabilities
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE
PrivateTmp=yes
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/etc/gdhcp /var/lib/gdhcp /var/log/gdhcp

[Install]
WantedBy=multi-user.target
```

Capabilities (`CAP_NET_ADMIN`, `CAP_NET_RAW`, `CAP_NET_BIND_SERVICE`) allow gdhcp to:
- Bind to port 67 (DHCP, normally privileged)
- Send gratuitous ARP for VIP failover
- Manage kernel routing for HA

### Appendix: Database Schema

Summarized; see [db/schema.sql](db/schema.sql) for DDL:

- `users` — Admin, operator, viewer accounts
- `api_tokens` — Bearer tokens for programmatic access
- `audit_logs` — CRUD audit trail
- `vrfs` — Virtual Routing Domains
- `ipam_prefixes` — CIDR blocks (aggregates, subnets, containers)
- `vlan_groups`, `vlans` — Layer-2 segmentation
- `dhcp_scopes` — Subnets with DHCP service
- `dhcp_pools` — IP ranges within scopes
- `dhcp_leases` — Allocated IPs and their bindings
- `dhcp_reservations` — Static IP assignments (MAC/hostname/etc.)
- `dhcp_options`, `dhcp_templates` — Option sets
- `entity_tags` — Free-form labels on prefixes, VLANs, leases

All rows have `created_at`, `updated_at` timestamps.

### Appendix: REST API

Full reference at [api/endpoints.md](api/endpoints.md). Base path: `/api/v1`.

Endpoints follow REST conventions:
- `GET /resources` — List (paginated, filterable)
- `GET /resources/{id}` — Describe
- `POST /resources` — Create
- `PUT /resources/{id}` or `PATCH /resources/{id}` — Update
- `DELETE /resources/{id}` — Delete

All requests require a bearer token (except `/auth/login` and `/health`).

Example:
```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/scopes | jq .
```

### Appendix: Getting Help

- **Docs:** Start with the [README.md](../README.md) (quick start) and [docs/README.md](README.md) (index)
- **Logs:** `journalctl -u gdhcp -f` (systemd) or `docker logs -f gdhcp` (Docker)
- **API errors:** Most errors include a `message` field and HTTP status code; see [api/endpoints.md](api/endpoints.md)
- **GitHub:** [github.com/shamalawy/gdhcp](https://github.com/shamalawy/gdhcp)

---

**End of Manual**

This manual covers the core operator workflows: install, configure, manage IPAM/DHCP, set up HA, and operate day-to-day. For architecture and design rationale, see the deep-dive docs. For troubleshooting specific to your deployment model, see [deploy/troubleshooting.md](deploy/troubleshooting.md).
