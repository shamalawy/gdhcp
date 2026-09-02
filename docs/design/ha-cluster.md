# HA Cluster Design for gdhcp

## Executive Summary

The gdhcp HA cluster enables 2+ servers to share DHCP load and tolerate server failure via:
- **VIP failover**: PostgreSQL-backed lease-based election (not VRRP) with netlink VIP assignment and gratuitous ARP
- **Concurrent DHCP serving**: both servers answer DHCP; lease allocation is atomic in PostgreSQL, so load-sharing is safe
- **Split-brain avoidance**: heartbeat fencing prevents both peers from holding the VIP when DB is unreachable
- **Config propagation**: PostgreSQL LISTEN/NOTIFY enables near-instant config reload across the cluster
- **Failover timeline**: 2–5 seconds (configurable heartbeat check interval, default 1 second)

This design keeps HA logic in the main gdhcp binary, avoids extra daemons, and leverages PostgreSQL as the single source of truth for state.

---

## VIP Implementation: Postgres-Based Lease Election vs. VRRP

### Decision: PostgreSQL-Based Lease + Netlink + Gratuitous ARP

We choose the Postgres lease approach over VRRP because:

1. **Single source of truth**: All cluster state (heartbeats, VIP assignment, config) lives in PostgreSQL, which the operator already operates in HA mode (Postgres HA is out of scope for gdhcp).
2. **No extra daemon**: VRRP requires a separate daemon (keepalived, ucarp) or in-kernel VRRP stack (Linux `ip_vrrp`, less portable).
3. **Simpler logic**: Primary/secondary election reduces to "lowest node_id with recent heartbeat."
4. **Handles network partitions**: Heartbeat fencing ensures both servers can't hold VIP if DB unreachable.
5. **Integrated with gdhcp binary**: No new deployment footprint.

### Rejected: VRRP

| Aspect | VRRP | Postgres Lease |
|--------|------|----------------|
| Failover time | 3–5 sec (1 sec intervals, 3–5 misses) | 1–5 sec (configurable check interval, default 1 sec) |
| Complexity | Separate daemon or kernel module | In-process, uses existing DB connection |
| Split-brain risk | Higher if network split but DB accessible | Lower: heartbeat fencing prevents dual-master |
| Dependency | Linux kernel VRRP support (may vary) | PostgreSQL (already required) |
| Operational overhead | Another service to monitor/restart | Part of gdhcp binary |

---

## Database Schema: Heartbeat & Cluster State

### Tables

#### `clusters`
```sql
CREATE TABLE clusters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    virtual_ip TEXT,  -- VIP address, e.g., "10.0.0.10/24"
    virtual_interface TEXT,  -- Interface to bind VIP, e.g., "eth0"
    heartbeat_interval_ms INT DEFAULT 1000,  -- ms between heartbeats
    heartbeat_ttl_ms INT DEFAULT 5000,  -- ms before heartbeat expires
    election_interval_ms INT DEFAULT 2000,  -- ms between election checks
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);
```

#### `cluster_servers`
```sql
CREATE TABLE cluster_servers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    node_name TEXT NOT NULL,  -- hostname or ID
    node_id INT NOT NULL,  -- used for tie-breaking in elections (lower wins)
    api_address TEXT NOT NULL,  -- e.g., "192.168.1.10:8080"
    dhcp_address TEXT NOT NULL,  -- e.g., "0.0.0.0:67"
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE(cluster_id, node_name),
    UNIQUE(cluster_id, node_id)
);
```

#### `cluster_heartbeat`
```sql
CREATE TABLE cluster_heartbeat (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    server_id UUID NOT NULL REFERENCES cluster_servers(id) ON DELETE CASCADE,
    heartbeat_time TIMESTAMP NOT NULL,
    role TEXT NOT NULL DEFAULT 'SECONDARY',  -- 'PRIMARY' or 'SECONDARY'
    vip_assigned BOOLEAN NOT NULL DEFAULT false,
    lease_version INT NOT NULL DEFAULT 0,  -- incremented by primary on config change
    last_config_version INT NOT NULL DEFAULT 0,  -- version of config this server has applied
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE(cluster_id, server_id)
);
CREATE INDEX idx_cluster_heartbeat_time ON cluster_heartbeat(cluster_id, heartbeat_time DESC);
```

#### `cluster_election_log`
```sql
CREATE TABLE cluster_election_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    elected_server_id UUID NOT NULL REFERENCES cluster_servers(id),
    elected_at TIMESTAMP NOT NULL DEFAULT now(),
    reason TEXT  -- e.g., 'heartbeat_expiry', 'new_election', 'manual_override'
);
```

### Key Constraints

- `heartbeat_ttl_ms` must be at least 2× `heartbeat_interval_ms` to avoid spurious failovers.
- `election_interval_ms` should be ≤ `heartbeat_ttl_ms / 2` for prompt detection.
- Only the PRIMARY role can update its own heartbeat; SECONDARY cannot.
- Heartbeat updates must be atomic and timestamped server-side to avoid clock-skew issues.

---

## Linux Capabilities

To manage the VIP, gdhcp binary needs:

```
CAP_NET_ADMIN    - for netlink (ip link, ip addr) operations
CAP_SYS_NICE     - for setuid/privilege escalation if running as non-root
```

### Deployment

**systemd Service (bare metal):**
```ini
[Service]
AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN CAP_SYS_NICE
SecureBits=keep-caps keep-caps-locked no-setuid-fixup no-setuid-fixup-locked
```

**Docker / Compose:**
```yaml
cap_add:
  - NET_ADMIN
```

**Kubernetes:**
```yaml
securityContext:
  capabilities:
    add:
      - NET_ADMIN
```

If running as non-root, add `setcap`:
```bash
sudo setcap cap_net_admin=ep /usr/local/bin/gdhcp
```

---

## Primary/Secondary Election & Heartbeats

### Election Algorithm

**Elected Primary**: the server with the lowest `node_id` among those with a `heartbeat_time` more recent than `now() - heartbeat_ttl_ms`.

**Election performed by**: every server independently, every `election_interval_ms` milliseconds (default 2 seconds).

**Tie-breaking**: if multiple servers have the same `node_id`, the one with the most recent `heartbeat_time` wins; if tied, the one added first (by `created_at` in `cluster_servers`) wins.

### Heartbeat Mechanism

1. **Primary server**: updates its row in `cluster_heartbeat` every `heartbeat_interval_ms` (default 1 second).
   - Sets `heartbeat_time = now()` and `role = 'PRIMARY'`.
   - Atomically sets `vip_assigned = true` (only primary).

2. **Secondary server**: does **not** write heartbeats; it only reads them.
   - Detects primary via election algorithm.
   - Waits in SECONDARY state.

3. **On heartbeat failure** (primary can't write to DB):
   - Primary detects via failed UPDATE and enters ERROR state.
   - Stops updating the heartbeat.
   - After `heartbeat_ttl_ms`, secondary election discovers primary is dead.
   - New primary takes over.

### Heartbeat Update Query

```sql
-- Executed by PRIMARY every heartbeat_interval_ms
UPDATE cluster_heartbeat
SET
    heartbeat_time = now(),
    role = 'PRIMARY',
    updated_at = now()
WHERE cluster_id = $1 AND server_id = $2
RETURNING heartbeat_time;
```

If this UPDATE affects 0 rows, the primary has crashed and was garbage-collected. Primary enters ERROR state and stops serving.

### Election Query

```sql
-- Executed by every server every election_interval_ms
WITH ranked_servers AS (
    SELECT
        ch.server_id,
        cs.node_id,
        ch.heartbeat_time,
        ROW_NUMBER() OVER (ORDER BY cs.node_id ASC, ch.heartbeat_time DESC, cs.created_at ASC) AS rank
    FROM cluster_heartbeat ch
    JOIN cluster_servers cs ON ch.server_id = cs.id
    WHERE ch.cluster_id = $1
      AND ch.heartbeat_time > now() - interval '1 millisecond' * $2  -- heartbeat_ttl_ms
)
SELECT server_id FROM ranked_servers WHERE rank = 1;
```

---

## Split-Brain Avoidance

### Scenario 1: Both Peers See DB, Network Between Them Down

**Heartbeat fencing prevents split-brain:**
- Primary updates heartbeat every 1 second.
- Secondary reads heartbeats via election query.
- When network splits, only one server's heartbeat is recent; election selects it uniquely.
- The other server becomes SECONDARY automatically (no VIP held).
- **Result**: only one VIP holder. **No split-brain.**

### Scenario 2: Both Peers See DB, Both Heartbeats Expired

**This cannot happen** if at least one peer is alive:
- If primary is alive, it updates heartbeat continuously.
- If primary crashes, its heartbeat expires, but secondary is still alive and will elect itself.
- After `heartbeat_ttl_ms`, secondary detects primary dead, becomes PRIMARY, assigns VIP.

**If both are down**: both heartbeats expire, no server in the cluster can update VIP. Load balancer or client must route to offline cluster or failover to alternate cluster. **No split-brain (both offline).**

### Scenario 3: Neither Peer Sees DB

**Heartbeat fencing ensures safety:**
- Primary cannot write heartbeat; detects this via UPDATE failure.
- Enters ERROR state, stops updating heartbeat, stops serving DHCP.
- Secondary cannot read election query; waits (does not assume PRIMARY).
- After `heartbeat_ttl_ms`, secondary's last known election result expires (in-process cache).
- Both enter WAITING_ELECTION state, do not serve DHCP, do not hold VIP.
- Once DB recovers, next election wins and new primary takes VIP.

**Risk**: during DB downtime, DHCP service is unavailable. **Mitigation**: operator runs Postgres HA (replication, failover, etc.) to minimize downtime.

### Scenario 4: One Peer Cannot See DB, the Other Can

**Isolated peer stops serving:**
- Peer with DB access: primary continues heartbeat, secondary sees it, no issue.
- Peer without DB access: cannot read election query, cannot update heartbeat (if primary). After `heartbeat_ttl_ms`, in-process election cache expires.
  - If it was primary: stops serving after cache expires.
  - If it was secondary: never served, no change.
- **Result**: only DB-connected peer serves DHCP. **No split-brain.**

---

## Concurrent DHCP Serving & Load-Sharing

### Design: Both Servers Answer DHCP

Because DHCP lease allocation is atomic in PostgreSQL — a single `INSERT ... ON CONFLICT (pool_id, ip_address) DO NOTHING` against the `UNIQUE (pool_id, ip_address)` constraint on `dhcp_leases` — both servers can safely:
1. Answer DISCOVER packets (both send OFFER).
2. Accept REQUEST packets (first to INSERT wins; the second sees zero rows affected and retries with a different candidate).

> **The constraint is on the address, not on the client.** Two nodes racing the same client's DISCOVER cannot hand the same address to different clients, which is the property that makes concurrent serving safe. They *can* pick two different candidate addresses for that one client and offer both — see the note under step 3 below.

### DHCP Packet Flow

#### Client DISCOVER (Broadcast)

1. Both primary and secondary receive the broadcast.
2. Both query `pool_leases` to check if client already has a lease (XID tracking).
3. Both craft OFFER packets. They carry the same options, but **not necessarily the same address**: when the client has no existing lease, each node runs its own candidate scan, and two scans that interleave can settle on two different free addresses. Only one of them is claimed at REQUEST; the other is left as an unclaimed row that the reaper expires.
4. Both send OFFER back to client.
5. Client may receive 2 OFFERs; this is normal (DHCP spec allows multiple offers).
6. Client picks one and sends REQUEST.

**Broadcast handling**: Both servers answer DISCOVER. Duplicate OFFERs are harmless; the client chooses.

#### Client REQUEST (Unicast to Picked Server, or Broadcast if Picking)

1. Whichever server sent the OFFER the client chose receives the REQUEST.
2. Server attempts to atomically INSERT into `pool_leases`:
   ```sql
   INSERT INTO pool_leases (client_mac, scope_id, ip_address, lease_time, xid, created_at)
   VALUES ($1, $2, $3, $4, $5, now())
   ON CONFLICT DO NOTHING;
   ```
3. If INSERT succeeds: server sends ACK.
4. If INSERT fails (constraint violation): another server beat us. Server sends NAK (or stays silent, client re-discovers).

**Outcome**: one server wins the lease; both servers see the same lease via subsequent queries. **No double-allocation.**

### State Machine for DHCP

Each DISCOVER/REQUEST is handled independently; no coordination needed between servers.

**In PRIMARY state**: answer all DHCP packets (DISCOVER, REQUEST, RELEASE, DECLINE).

**In SECONDARY state**: answer all DHCP packets (same as PRIMARY).
- Secondaries are "hot," not passive.
- If a secondary has an offer in flight when it becomes primary, that offer is valid (both primary/secondary use same lease DB).

**In FAILOVER, WAITING_ELECTION, INITIALIZING, or ERROR states**: do not answer DHCP (except RELEASE/DECLINE, which are safe to ignore or answer).

### Config Change Propagation via Lease Version

When primary updates cluster config (e.g., DHCP option sets, pool sizes):

1. Primary increments `lease_version` in its `cluster_heartbeat` row.
2. Primary writes new config to `dhcp_config` table (app-specific table with `version` column).
3. Secondary detects `lease_version` change via heartbeat read.
4. Secondary queries `dhcp_config` table with the new version and reloads.
5. Both servers now serve with updated config.

**Timing**: within 1–2 seconds (next heartbeat read/election cycle).

---

## Config Change Propagation: PostgreSQL LISTEN/NOTIFY

### Mechanism

1. **Trigger on config update**: whenever `dhcp_config` is updated, a trigger fires:
   ```sql
   CREATE FUNCTION notify_config_change() RETURNS TRIGGER AS $$
   BEGIN
       PERFORM pg_notify('dhcp_config_change', json_build_object(
           'cluster_id', NEW.cluster_id,
           'version', NEW.version,
           'updated_at', NEW.updated_at
       )::text);
       RETURN NEW;
   END;
   $$ LANGUAGE plpgsql;
   
   CREATE TRIGGER trigger_notify_config ON dhcp_config
   AFTER UPDATE FOR EACH ROW EXECUTE FUNCTION notify_config_change();
   ```

2. **In gdhcp**: maintain a persistent PostgreSQL connection that listens on `dhcp_config_change`.

3. **On notification**:
   - All servers (primary and secondary) receive the notification asynchronously.
   - Each server re-reads the config for its cluster.
   - Servers that are in ERROR or WAITING_ELECTION state ignore (they don't serve anyway).

4. **Fallback**: even if NOTIFY fails, each server re-reads config every `config_check_interval_ms` (e.g., 30 seconds).

**Timing**: sub-second (PostgreSQL NOTIFY is instant); typical propagation < 100 ms.

### LISTEN/NOTIFY Connection

Separate from the main app DB connection. Runs in a dedicated goroutine. If it drops, reconnect and re-LISTEN.

---

## Cluster & Server Health Reporting

### Health Status for UI

Each server exposes a health endpoint (`GET /api/v1/cluster/health/{cluster_id}`) that returns:

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
      "api_address": "192.168.1.10:8080",
      "dhcp_address": "0.0.0.0:67"
    },
    {
      "server_id": "uuid",
      "node_name": "dhcp-02",
      "node_id": 2,
      "role": "SECONDARY",
      "vip_assigned": false,
      "heartbeat_time": "2026-08-29T10:29:58Z",
      "heartbeat_age_ms": 2250,
      "is_healthy": true,
      "api_address": "192.168.1.11:8080",
      "dhcp_address": "0.0.0.0:67"
    }
  ],
  "cluster_state": "HEALTHY",
  "notes": ""
}
```

**`is_healthy`** for a server: `heartbeat_age_ms < heartbeat_ttl_ms`.

**`cluster_state`**:
- `HEALTHY`: one primary, ≥1 secondary, all heartbeats fresh.
- `DEGRADED`: one primary, no secondary (single point of failure).
- `NO_PRIMARY`: no server has a fresh heartbeat (DB down or all servers offline).
- `SPLIT_BRAIN`: multiple servers claim PRIMARY (should not happen; indicates bug or extreme clock skew).

### Metrics for Monitoring

Export Prometheus metrics:

- `gdhcp_cluster_state{cluster_id}` (gauge: 1=HEALTHY, 2=DEGRADED, 3=NO_PRIMARY, 4=SPLIT_BRAIN)
- `gdhcp_server_role{cluster_id, server_id, node_name}` (gauge: 1=PRIMARY, 0=SECONDARY)
- `gdhcp_server_heartbeat_age_ms{cluster_id, server_id, node_name}` (gauge)
- `gdhcp_vip_assigned{cluster_id, server_id, node_name}` (gauge: 1 if assigned, 0 otherwise)
- `gdhcp_cluster_failovers_total{cluster_id}` (counter: incremented on election change)
- `gdhcp_config_reload_lag_ms{cluster_id}` (gauge: max of all servers' config lag)

---

## Failover Timeline

Assuming default configuration:

| Step | Latency | Cumulative |
|------|---------|-----------|
| Primary heartbeat fails to write (network/DB issue) | — | 0 ms |
| Primary detects UPDATE failure | 0–50 ms | 50 ms |
| Primary enters ERROR state, stops serving DHCP | — | 50 ms |
| Secondary runs next election check (every 2 sec default) | 0–2000 ms | 2050 ms |
| Secondary detects primary heartbeat is stale | — | 2050 ms |
| Secondary becomes PRIMARY | — | 2050 ms |
| Secondary assigns VIP via netlink | 10–50 ms | 2100 ms |
| Secondary sends gratuitous ARP | 10–50 ms | 2150 ms |
| Clients' ARP caches updated, traffic reroutes to new primary | 50–500 ms | 2200–2650 ms |

**Total failover time: 2–5 seconds** (configurable via `election_interval_ms` and `heartbeat_ttl_ms`).

**To reduce to <1 second**: set `election_interval_ms = 200` and `heartbeat_ttl_ms = 1000`. Trade-off: more frequent DB queries.

---

## VIP Assignment & Gratuitous ARP

### Netlink-Based VIP Assignment

**On Primary Election:**

```go
// Pseudo-code
func (p *primaryRole) assignVIP(ctx context.Context, cluster *Cluster) error {
    // Create virtual interface (alias)
    link, err := netlink.LinkByName(cluster.VirtualInterface)
    if err != nil {
        return err
    }
    
    vipAddr, err := netlink.ParseAddr(cluster.VirtualIP)
    if err != nil {
        return err
    }
    
    // Add the VIP address to the interface
    err = netlink.AddrAdd(link, vipAddr)
    if err != nil && !strings.Contains(err.Error(), "file exists") {
        return err
    }
    
    // Mark as up
    err = netlink.LinkSetUp(link)
    if err != nil {
        return err
    }
    
    // Send gratuitous ARP
    return p.sendGratuitousARP(ctx, link, vipAddr)
}
```

### Gratuitous ARP

Using `libarp` or raw socket:

```go
func (p *primaryRole) sendGratuitousARP(ctx context.Context, link netlink.Link, addr *net.IPNet) error {
    // Send ARP reply with our MAC as target
    // ARP packet: "this is me, broadcast to all"
    // Typical: 3–5 copies with 100–200 ms spacing
    
    for i := 0; i < 3; i++ {
        // Send ARP packet
        // ...
        select {
        case <-time.After(200 * time.Millisecond):
        case <-ctx.Done():
            return ctx.Err()
        }
    }
    return nil
}
```

### VIP Removal on Demotion

When primary is demoted to secondary (e.g., new primary elected with lower `node_id`):

```go
func (s *secondaryRole) removeVIP(ctx context.Context, cluster *Cluster) error {
    link, err := netlink.LinkByName(cluster.VirtualInterface)
    if err != nil {
        return err
    }
    
    vipAddr, err := netlink.ParseAddr(cluster.VirtualIP)
    if err != nil {
        return err
    }
    
    // Remove the address
    return netlink.AddrDel(link, vipAddr)
}
```

**Safety**: VIP removal is non-fatal; if it fails, the server remains reachable but does not serve DHCP (secondary state).

---

## Go Package Layout & State Machine

### Package Structure

```
pkg/ha/
├── cluster.go          # Cluster manager, main HA loop
├── server.go           # Server (node) representation
├── election.go         # Primary/secondary election logic
├── heartbeat.go        # Heartbeat reader/writer
├── vip.go              # VIP assignment/removal (netlink + ARP)
├── state.go            # State machine (roles, transitions)
├── config_watch.go     # Config reload (LISTEN/NOTIFY + polling)
├── health.go           # Health status reporting
├── metrics.go          # Prometheus metrics export
└── db_schema.go        # DB initialization & schema

cmd/gdhcp-ha/
└── main.go             # HA cluster CLI (config, start, status, failover)
```

### State Machine

**States** (Go constants):

```go
const (
    StateInitializing   = "INITIALIZING"
    StateWaitingElection = "WAITING_ELECTION"
    StateSecondary      = "SECONDARY"
    StatePrimary        = "PRIMARY"
    StateFailover       = "FAILOVER"
    StateError          = "ERROR"
    StateShutdown       = "SHUTDOWN"
)
```

**State Interface** (roles):

```go
type Role interface {
    Name() string
    Enter(ctx context.Context) error
    Exit(ctx context.Context) error
    Handle(ctx context.Context, event Event) error
}

type Event struct {
    Type    string  // "heartbeat", "election", "db_error", "network_error", "shutdown"
    Payload interface{}
}
```

**Concrete Roles**:

```go
type InitializingRole struct { ... }
type WaitingElectionRole struct { ... }
type SecondaryRole struct { ... }
type PrimaryRole struct { ... }
type FailoverRole struct { ... }
type ErrorRole struct { ... }
type ShutdownRole struct { ... }
```

### State Transitions

```
INITIALIZING
  └─> [config loaded, DB connected]
      └─> WAITING_ELECTION

WAITING_ELECTION
  ├─> [elected primary] → PRIMARY
  ├─> [elected secondary] → SECONDARY
  └─> [election query fails] → ERROR

SECONDARY
  ├─> [elected to primary] → FAILOVER
  ├─> [heartbeat reader dies] → ERROR
  └─> [shutdown signal] → SHUTDOWN

PRIMARY
  ├─> [demoted to secondary] → FAILOVER
  ├─> [heartbeat write fails] → ERROR
  ├─> [VIP assignment fails] → ERROR
  └─> [shutdown signal] → SHUTDOWN

FAILOVER
  ├─> [old role exit complete, new role enter complete] → SECONDARY | PRIMARY
  └─> [error during transition] → ERROR

ERROR
  ├─> [retry timer fires, DB recovers] → WAITING_ELECTION
  └─> [shutdown signal] → SHUTDOWN

SHUTDOWN
  └─> [all goroutines stopped]
      └─> [exit]
```

### Cluster Manager Loop (Pseudocode)

```go
type ClusterManager struct {
    db          *sql.DB
    listenConn  *sql.Conn  // For LISTEN/NOTIFY
    currentRole Role
    config      *ClusterConfig
    // ...
}

func (cm *ClusterManager) Run(ctx context.Context) error {
    for {
        select {
        case event := <-cm.events:
            if err := cm.currentRole.Handle(ctx, event); err != nil {
                cm.transition(ctx, &ErrorRole{})
            }
        case <-cm.electionTicker:
            cm.runElection(ctx)
        case <-cm.heartbeatTicker:
            if cm.currentRole.Name() == StatePrimary {
                cm.writeHeartbeat(ctx)
            }
        case <-cm.configNotify:
            cm.reloadConfig(ctx)
        case <-ctx.Done():
            return cm.shutdown(ctx)
        }
    }
}

func (cm *ClusterManager) runElection(ctx context.Context) {
    electedServerID := cm.queryElection(ctx)
    if electedServerID == cm.localServerID {
        if cm.currentRole.Name() != StatePrimary {
            cm.transition(ctx, &FailoverRole{oldRole: cm.currentRole, newRole: &PrimaryRole{}})
        }
    } else {
        if cm.currentRole.Name() != StateSecondary {
            cm.transition(ctx, &FailoverRole{oldRole: cm.currentRole, newRole: &SecondaryRole{}})
        }
    }
}

func (cm *ClusterManager) transition(ctx context.Context, newRole Role) {
    oldRole := cm.currentRole
    
    if err := oldRole.Exit(ctx); err != nil {
        // Log error but continue
    }
    
    cm.currentRole = newRole
    
    if err := newRole.Enter(ctx); err != nil {
        cm.currentRole = &ErrorRole{err: err}
        cm.currentRole.Enter(ctx)
    }
}
```

### PrimaryRole Implementation (Sketch)

```go
type PrimaryRole struct {
    clusterID   string
    serverID    string
    heartbeatTTLMs int
}

func (p *PrimaryRole) Name() string {
    return StatePrimary
}

func (p *PrimaryRole) Enter(ctx context.Context) error {
    // 1. Assign VIP
    if err := p.assignVIP(ctx); err != nil {
        return err
    }
    
    // 2. Send gratuitous ARP
    if err := p.sendGratuitousARP(ctx); err != nil {
        return err
    }
    
    // 3. Update heartbeat to PRIMARY
    if err := p.updateHeartbeat(ctx, StatePrimary, true); err != nil {
        return err
    }
    
    // 4. Emit metric
    recordRoleChange(p.clusterID, StatePrimary)
    
    return nil
}

func (p *PrimaryRole) Exit(ctx context.Context) error {
    // 1. Stop answering DHCP (state check in DHCP handler)
    // 2. Remove VIP
    if err := p.removeVIP(ctx); err != nil {
        // Log but don't fail; secondary can take over even if VIP removal failed
        log.Warnf("failed to remove VIP: %v", err)
    }
    
    // 3. Mark as not primary in heartbeat (only if DB reachable)
    p.updateHeartbeat(ctx, StateSecondary, false)
    
    return nil
}

func (p *PrimaryRole) Handle(ctx context.Context, event Event) error {
    switch event.Type {
    case "heartbeat":
        // Heartbeat written successfully, update metrics
        return nil
    case "election":
        // If election says we're no longer primary, return error to trigger transition
        electedID := event.Payload.(string)
        if electedID != p.serverID {
            return fmt.Errorf("demoted by election")
        }
        return nil
    case "db_error":
        // DB connection lost; stop serving
        return event.Payload.(error)
    default:
        return nil
    }
}
```

### SecondaryRole Implementation (Sketch)

```go
type SecondaryRole struct {
    clusterID string
    serverID  string
}

func (s *SecondaryRole) Name() string {
    return StateSecondary
}

func (s *SecondaryRole) Enter(ctx context.Context) error {
    // 1. Ensure VIP is not assigned
    if err := s.removeVIP(ctx); err != nil {
        log.Warnf("failed to remove VIP (expected if not assigned): %v", err)
    }
    
    // 2. Do not write heartbeat; only read
    // 3. Answer DHCP normally (load-sharing with primary)
    
    recordRoleChange(s.clusterID, StateSecondary)
    return nil
}

func (s *SecondaryRole) Exit(ctx context.Context) error {
    // No cleanup needed; promote to primary will assign VIP
    return nil
}

func (s *SecondaryRole) Handle(ctx context.Context, event Event) error {
    switch event.Type {
    case "election":
        electedID := event.Payload.(string)
        if electedID == s.serverID {
            return fmt.Errorf("elected to primary")
        }
        return nil
    case "db_error":
        return event.Payload.(error)
    default:
        return nil
    }
}
```

---

## Initialization & Configuration

### Cluster Config File

`/etc/gdhcp/cluster.yaml` (or passed via flag):

```yaml
cluster:
  name: "us-east-1"
  virtual_ip: "10.0.0.10/24"
  virtual_interface: "eth0"
  heartbeat_interval_ms: 1000
  heartbeat_ttl_ms: 5000
  election_interval_ms: 2000

server:
  node_name: "dhcp-01"
  node_id: 1
  api_address: "0.0.0.0:8080"
  dhcp_address: "0.0.0.0:67"

database:
  url: "postgres://user:pass@postgres-primary:5432/gdhcp?sslmode=require"
  max_open_conns: 25
  max_idle_conns: 5

logging:
  level: "info"
```

### Initialization Steps

1. Load config from file.
2. Connect to PostgreSQL.
3. Run schema migrations (if not already run).
4. Validate heartbeat and election intervals (TTL >= 2× interval).
5. Enter INITIALIZING state.
6. Run first election.
7. Transition to PRIMARY or SECONDARY based on election result.
8. Start heartbeat/election/config watch loops.
9. Start DHCP server listeners.

### CLI Commands

```bash
# Start the cluster member
gdhcp-ha cluster start --config /etc/gdhcp/cluster.yaml

# Show cluster health
gdhcp-ha cluster status --cluster-id us-east-1

# Manually trigger failover (testing)
gdhcp-ha cluster failover --cluster-id us-east-1 --to-server dhcp-02

# List cluster members
gdhcp-ha cluster list-members --cluster-id us-east-1

# Show HA logs
gdhcp-ha cluster logs --cluster-id us-east-1 --tail 100
```

---

## Error Handling & Recovery

### Primary Heartbeat Write Fails

**Cause**: DB connection lost, network partition, or PostgreSQL down.

**Action**:
1. Log error.
2. Emit metric `gdhcp_heartbeat_write_errors_total`.
3. Retry up to 3 times with backoff.
4. If all retries fail: transition to ERROR state, stop serving DHCP.
5. Stop VIP assignment.
6. After `heartbeat_ttl_ms`, secondary detects primary dead and takes over.

### Election Query Fails

**Cause**: DB connection lost.

**Action**:
1. Log error.
2. Retain current role (primary or secondary) for up to `heartbeat_ttl_ms`.
3. Retry election query on next tick.
4. If DB remains unavailable after TTL: transition to ERROR, stop serving.

### VIP Assignment Fails

**Cause**: netlink error, permission denied, or invalid interface.

**Action**:
1. Log error.
2. Emit metric.
3. Transition to ERROR state (critical; cannot be primary without VIP).
4. Secondary will take over on next election.

### Gratuitous ARP Fails

**Cause**: raw socket error, permission denied.

**Action**:
1. Log warning (non-fatal).
2. Continue as primary; clients may take longer to update ARP caches.
3. Emit metric.

### Config Reload Fails

**Cause**: malformed config, missing file, or invalid settings.

**Action**:
1. Log error.
2. Retain old config.
3. Emit metric `gdhcp_config_reload_errors_total`.
4. Retry on next config watch or polling interval.
5. Do not transition to ERROR; DHCP continues with old config.

---

## Testing Strategy

### Unit Tests

- Election algorithm (with mock heartbeat data).
- State machine transitions.
- Heartbeat TTL logic.
- VIP assignment/removal.

### Integration Tests

- Postgres-backed heartbeat (with test DB).
- Config propagation (LISTEN/NOTIFY).
- Primary/secondary failover.
- Concurrent DHCP lease allocation.
- Split-brain detection.

### E2E Tests (Docker Compose)

- Cluster of 2 servers + PostgreSQL.
- Kill primary server, verify secondary takes VIP within 5 seconds.
- Kill secondary server, verify primary continues.
- Network partition between servers, verify no split-brain.
- PostgreSQL down, verify both servers stop serving after TTL.

---

## Operational Considerations

### Monitoring & Alerting

1. **Alert on `cluster_state != HEALTHY`**: no primary, split-brain, degraded.
2. **Alert on `heartbeat_age_ms > heartbeat_ttl_ms`**: server dead.
3. **Alert on `vip_assigned` changes**: failover event.
4. **Alert on `config_reload_lag_ms > 5000`**: config not synced.

### Backup & Restore

- PostgreSQL HA (replication, snapshots) is the operator's responsibility.
- Cluster state (heartbeat, VIP assignment) is transient and reconstructed on restart.
- Lease database is in PostgreSQL; restore from backups as needed.

### Scaling

- Cluster can have 2–N servers.
- Election algorithm scales; query time is O(N).
- For >10 servers, consider sharding clusters (separate `cluster_id`).

### Manual Intervention

**Force failover** (if secondary is stuck):
```sql
-- Primary manually updates secondary's heartbeat to simulate secondary's election
UPDATE cluster_heartbeat
SET heartbeat_time = now()
WHERE server_id = <secondary_id>;

-- Then demote self
DELETE FROM cluster_heartbeat WHERE server_id = <primary_id>;
```

**Reset cluster** (all servers offline, need to restart):
1. Delete all rows in `cluster_heartbeat`.
2. Restart all servers.
3. Lowest `node_id` will be elected primary.

---

## Future Enhancements

1. **Multi-primary (active-active)**: each server holds unique IP ranges. Requires more complex lease coordination.
2. **Automatic VIP migration**: detect primary health independently and failover without DB (harder to reason about; rejected for v1).
3. **Witness server**: a 3rd server that votes on primary/secondary but does not serve DHCP (avoids 50/50 split votes in 2-node clusters).
4. **Persistent heartbeat log**: audit trail of failovers (via `cluster_election_log`).
