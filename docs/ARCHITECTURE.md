# gdhcp Architecture (Final, Binding)

**Status**: Final binding architecture contract v1.0  
**Date**: 2026-08-29  
**Repo**: github.com/shamalawy/gdhcp  
**Module Path**: github.com/shamalawy/gdhcp

---

## Executive Summary

gdhcp is a high-availability (HA) DHCP server with integrated IPAM, built in Go with PostgreSQL backing. It supports:

- **Full DHCPv4** with all IANA options (codes 1–254), including relay agent support (Option 82)
- **VRF-aware** serving with overlapping CIDR support per VRF
- **HA clustering** with Postgres-backed lease-based VIP election, no VRRP or keepalived
- **Atomic lease allocation** via serializable transactions, safe for concurrent server load-sharing
- **Option inheritance** (global → template → scope → pool → reservation) with template priorities
- **Template system** for reusable DHCP settings
- **Comprehensive IPAM** with prefix hierarchy, searchable, tag/label support
- **Observability**: metrics (Prometheus), dashboards, live utilization, historical stats, audit logs
- **Single binary** deployment (Angular frontend embedded via go:embed)
- **Three deployment targets**: bare metal (systemd), Docker Compose, Kubernetes

**Single source of truth**: PostgreSQL. No state on disk; all config read from DB on startup; migrations run at boot.

---

## Component Architecture

### Mermaid Diagram

```mermaid
graph TB
    subgraph Client["DHCP Clients (Network)"]
        C1["DHCP Client 1 (MAC: aa:bb:cc:dd:ee:ff)"]
        C2["DHCP Client 2"]
        C3["Relay Agent (Option 82)"]
    end

    subgraph HA["HA Cluster (Postgres-Backed)"]
        subgraph Primary["Server A (Primary Role)"]
            DHCPEng1["DHCP Engine<br/>(listener.go, dora.go)"]
            HADB1["HA Manager<br/>(election.go, vip.go)"]
            VIP["VIP Assigned<br/>10.0.0.10/24"]
        end
        subgraph Secondary["Server B (Secondary Role)"]
            DHCPEng2["DHCP Engine<br/>(listener.go, dora.go)"]
            HADB2["HA Manager<br/>(election.go, vip.go)"]
        end
        subgraph ConnPool["Connection Pool<br/>(main DB conn)"]
            DB[(PostgreSQL<br/>DHCP/IPAM DB)]
        end
        subgraph Heartbeat["Heartbeat & Election<br/>(cluster_heartbeat table)"]
            HB["Heartbeat/Election Logic<br/>(every 1–2 seconds)"]
        end
    end

    subgraph API["REST API & CLI"]
        APILayer["api/handler.go<br/>(router, middleware)"]
        CLILayer["cli/command.go<br/>(cobra subcommands)"]
    end

    subgraph Store["Store Layer (Repository Pattern)"]
        LeaseStore["LeaseStore<br/>(allocate, renew, expire)"]
        ConfigStore["ConfigStore<br/>(scopes, pools, options)"]
        IPAMStore["IPAMStore<br/>(prefixes, vrfs)"]
    end

    subgraph Frontend["Frontend (Embedded Angular)"]
        GUI["GUI (Angular app)"]
        Dashboard["Dashboard<br/>(utilization, status)"]
        IPAMTree["IPAM Tree Nav<br/>(prefix hierarchy)"]
    end

    Client -->|UDP 67/68| DHCPEng1
    Client -->|UDP 67/68| DHCPEng2
    Client -->|Option 82| DHCPEng1
    Client -->|Option 82| DHCPEng2

    DHCPEng1 -->|Allocate/Renew| LeaseStore
    DHCPEng2 -->|Allocate/Renew| LeaseStore
    DHCPEng1 -->|Fetch scopes/options| ConfigStore
    DHCPEng2 -->|Fetch scopes/options| ConfigStore

    LeaseStore -->|Read/Write leases| DB
    ConfigStore -->|Read config| DB
    IPAMStore -->|Read/Write IPAM| DB
    HB -->|Write heartbeat| DB
    HB -->|Election query| DB

    HADB1 -->|Update role/VIP| HB
    HADB2 -->|Read election| HB
    HADB1 -->|Assign VIP| VIP
    HADB2 -->|Listen for failover| HB

    APILayer -->|Read/Write| LeaseStore
    APILayer -->|Read/Write| ConfigStore
    APILayer -->|Read/Write| IPAMStore
    CLILayer -->|Read/Write| LeaseStore
    CLILayer -->|Read/Write| ConfigStore
    CLILayer -->|Read/Write| IPAMStore

    Frontend -->|HTTP (JSON)| APILayer
    GUI -->|Navigate tree| IPAMTree
    Dashboard -->|Live stats| APILayer

    ConnPool -->|Postgres driver| DB
    APILayer -->|Serve| Frontend

    style VIP fill:#0f0,color:#fff
    style DB fill:#00f,color:#fff
    style DHCPEng1 fill:#f90,color:#000
    style DHCPEng2 fill:#f90,color:#000
    style HADB1 fill:#f00,color:#fff
    style HADB2 fill:#f00,color:#fff
```

---

## Critique Resolution

This section resolves every blocker and major critique point from the adversarial review.

### Blocker: Lease Allocation Safety (DORA Race)

**Problem**: Concurrent DORA allocation between servers causes race condition. `SELECT ... FOR UPDATE` not explicitly shown; if two servers SELECT the same free IP, both try UPDATE, causing `SERIALIZATION_FAILURE` error with no retry.

**Resolution**:

1. **Explicit SELECT ... FOR UPDATE in loop**:  
   Allocation pseudocode (in `internal/dhcp/allocation.go`) wraps entire flow in `SERIALIZABLE` isolation level:

   ```go
   func (e *Engine) allocateLease(ctx context.Context, pool *uuid.UUID, clientMAC string, clientID string) (*Lease, error) {
     // Retry loop: up to 3 attempts on serialization errors
     for attempt := 0; attempt < 3; attempt++ {
       txn, _ := e.db.BeginTx(ctx, &sql.TxOptions{
         Isolation: sql.LevelSerializable,
       })

       // Step 1: Check for existing lease
       existing, _ := txn.GetLeaseByClient(ctx, clientMAC, clientID)
       if existing != nil && !existing.Expired() {
         txn.Commit()
         return existing, nil
       }

       // Step 2: SELECT first free IP FOR UPDATE, LIMIT 1
       var freeIP *net.IP
       rows, _ := txn.QueryContext(ctx, `
         SELECT ip_address FROM dhcp_leases
         WHERE pool_id = $1
           AND binding_state IN ('released', 'expired')
           AND lease_end < NOW()
         ORDER BY lease_end ASC
         LIMIT 1
         FOR UPDATE SKIP LOCKED
       `, pool)
       if !rows.Next() {
         txn.Rollback()
         if attempt < 2 {
           time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
           continue
         }
         return nil, ErrNoLeases
       }
       rows.Scan(&freeIP)

       // Step 3: UPDATE with condition
       leaseEnd := time.Now().Add(e.pool.LeaseTime)
       result, err := txn.ExecContext(ctx, `
         UPDATE dhcp_leases
         SET binding_state = 'active', client_mac = $1, client_id = $2,
             lease_start = NOW(), lease_end = $3, binding_server_id = $4
         WHERE pool_id = $5 AND ip_address = $6 AND binding_state IN ('released', 'expired')
       `, clientMAC, clientID, leaseEnd, e.serverID, pool, freeIP)

       if err != nil {
         // Catch SERIALIZATION_FAILURE specifically
         if isSerializationError(err) {
           txn.Rollback()
           if attempt < 2 {
             time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
             continue
           }
           return nil, fmt.Errorf("allocation conflict after 3 retries: %w", err)
         }
         txn.Rollback()
         return nil, err
       }

       if result.RowsAffected == 0 {
         txn.Rollback()
         if attempt < 2 {
           time.Sleep(10 * time.Millisecond)
           continue
         }
         return nil, ErrNoLeases
       }

       txn.Commit()
       return &Lease{...}, nil
     }
     return nil, ErrAllocationFailed
   }
   ```

2. **Retry logic with exponential backoff** (up to 3 retries, 10–50ms sleep).

3. **Code comment linking to RFC 2131 §3.1** in allocation.go: *"RFC 2131 §3.1: Only one server must win a lease. Serializable isolation guarantees this."*

4. **Configuration requirement**: Schema requires `LevelSerializable` for all lease allocation transactions. Documented in deploy guide.

---

### Blocker: Heartbeat Clock Skew

**Problem**: Heartbeat TTL logic compares `heartbeat_time > now() - interval` using server-local clocks. Clock skew causes false failovers.

**Resolution**:

1. **Mandate NTP sync in deployment guide** (deploy-build.md §Bare Metal and §Kubernetes):
   - Required: `ntpd` or `chrony` running and synchronized
   - Acceptable skew: max 500ms across cluster servers (enforced via monitoring)
   - Example: `timedatectl status` on Linux; ensure `System clock synchronized: yes`

2. **Wider TTL margin for clock skew**:
   - Election query (ha-cluster.go):
   ```sql
   WHERE heartbeat_time > now() - interval '1 millisecond' * ($heartbeat_ttl_ms * 2)
   ```
   This allows up to 2× configured TTL to account for clock skew. Default: `heartbeat_ttl_ms = 3000`, so query tolerates 6 seconds of skew.

3. **Server-side timestamp generation** (recommended):
   - Heartbeat write (`UPDATE cluster_heartbeat SET heartbeat_time = now()`) uses PostgreSQL's `now()`, not client-local time.
   - This centralizes time reference to DB server.

4. **Prominent documentation in deploy-build.md §Prerequisites**:
   > "NTP REQUIREMENT: All cluster servers MUST have synchronized system clocks within 500ms of each other. Enable ntpd/chrony and verify via `timedatectl status`. Clock skew exceeding 500ms will cause false failovers and loss of DHCP service. Alert: Clock skew detected when `heartbeat_age_ms` exceeds 2× heartbeat_ttl_ms."

---

### Blocker: DHCP State During Database Partition

**Problem**: Primary heartbeat fails but primary continues serving DHCP. Race between detection and state transition. Partition recovery causes unnecessary ERROR state.

**Resolution**:

1. **Fast-path heartbeat check before DHCP response**:
   ```go
   func (e *Engine) handleDHCPPacket(ctx context.Context, pkt *DHCPPacket) error {
     // Before answering any DHCP packet, verify primary can write heartbeat
     if e.ha.currentRole == "PRIMARY" {
       if time.Since(e.ha.lastHeartbeatWrite) > e.ha.heartbeatIntervalMs {
         return errors.New("heartbeat stale; refusing to serve DHCP")
       }
     }
     // Continue with normal DHCP handling
     return e.dhcp.handlePacket(ctx, pkt)
   }
   ```

2. **Exponential backoff for heartbeat retries** (before ERROR state):
   - Initial retry: 100ms
   - Exponential: 200ms, 400ms, 800ms (capped at 1 second)
   - After 5 failed retries over ~2 seconds, transition to ERROR
   - This prevents spurious ERROR on brief DB latency

3. **DEGRADED state** (future enhancement, optional):
   - Primary can enter DEGRADED if heartbeat slow (>500ms latency)
   - DEGRADED state: answer DHCP but rate-limit (favor secondary)
   - Current design: simpler; only PRIMARY and ERROR states

4. **Implementation**: ha-cluster.go `heartbeatLoop()` function.

---

### Blocker: VIP Assignment Ordering and Netlink Race

**Problem**: VIP assignment and response sending not synchronized. If assignment fails, primary stays PRIMARY but unreachable. Gratuitous ARP race.

**Resolution**:

1. **Guarantee VIP assigned BEFORE DHCP answering**:
   - VIP assignment gated by latch/flag in DHCP handler
   ```go
   type Engine struct {
     vipReady atomic.Bool  // Set only after VIP assignment succeeds
   }

   func (e *Engine) handleDHCPPacket(...) error {
     if e.ha.isPrimary() && !e.vipReady.Load() {
       return errors.New("VIP not yet assigned; refusing DHCP")
     }
     // ... continue
   }

   func (e *HA) assignVIP() error {
     if err := e.netlink.AddrReplace(e.vipAddr, e.vipIface); err != nil {
       e.transition(ERROR)  // Enter ERROR state if assignment fails
       e.vipReady.Store(false)
       return err
     }
     if err := e.sendGratuitousARP(...); err != nil {
       // Log, alert, but don't fail; ARP is best-effort
       e.metrics.incVIPARPFailures()
     }
     e.vipReady.Store(true)  // Only now allow DHCP
     return nil
   }
   ```

2. **Use netlink.AddrReplace() for idempotency**:
   - Handles "address already exists" error gracefully
   - Replaces instead of adding, preventing duplicates

3. **Gratuitous ARP retry** (3 attempts, exponential backoff):
   ```go
   for i := 0; i < 3; i++ {
     if err := e.sendGratuitousARP(); err == nil {
       return nil
     }
     if i < 2 {
       time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
     }
   }
   ```

4. **Prometheus metric** `gdhcp_vip_assignment_failures_total` (counter) and alert if > 0 in 5 minutes.

5. **Libraries**: 
   - Netlink: `github.com/vishvananda/netlink` v1.2.0+
   - Gratuitous ARP: `github.com/mdlayher/arp` v1.5.0+ (or raw socket if needed)

---

### Major: Election Query Hang Under Load

**Problem**: Election query has no timeout. If DB query hangs, secondary misses failover detection window.

**Resolution**:

1. **Add context timeout to election query** (ha-cluster.go):
   ```go
   ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
   defer cancel()
   
   rows, err := e.db.QueryContext(ctx, electionQuery, ...)
   if err == context.DeadlineExceeded {
     // Query timed out; treat as DB unreachable
     e.lastElectionResult = e.lastKnownElection  // Use cached result
     return
   }
   ```

2. **If query times out**: retain current role (don't change state). Use last known election result (cached for up to `heartbeat_ttl_ms`).

3. **Emit metric** `gdhcp_election_query_duration_ms` (histogram) with bucketing: 10, 50, 100, 200, 500ms.

4. **Alert threshold**: WARN if > 100ms, ALERT if > 500ms (over 5-minute window).

5. **Log slow queries** with stacktrace for debugging (via OpenTelemetry tracing, optional).

---

### Major: Config Reload Race on Secondary

**Problem**: Secondary detects `lease_version` change but old config served while reloading.

**Resolution**:

1. **Atomic config reload** (config-manager.go):
   ```go
   func (cm *ConfigManager) ReloadConfig(ctx context.Context, version int) error {
     // Load entire config into new in-memory structure
     newConfig, err := cm.store.LoadConfigVersion(ctx, version)
     if err != nil {
       return err
     }
     
     // Atomic swap via sync.Mutex or sync/atomic
     cm.mu.Lock()
     cm.current = newConfig
     cm.currentVersion = version
     cm.mu.Unlock()
     
     return nil
   }

   func (e *Engine) getConfig() *Config {
     e.configMgr.mu.RLock()
     defer e.configMgr.mu.RUnlock()
     return e.configMgr.current
   }
   ```

2. **Version-gate DHCP requests** (dhcp-engine.go):
   ```go
   func (e *Engine) handleDHCPPacket(...) error {
     config := e.getConfig()
     if config.Version < e.ha.leaseVersion {
       // Config reload in progress; refuse with NAK
       return ErrConfigMismatch
     }
     // Continue with current config
   }
   ```

3. **DHCP handler middleware** acquires read lock on config before handling requests (see above `getConfig()`).

4. **Prometheus metric** `gdhcp_config_reload_duration_ms` (histogram) and alert if > 1000ms.

---

### Major: Failover Timeline Assumes Healthy DB

**Problem**: 2–5 second failover timeline assumes DB reachable by secondary. If both servers experience DB partition, secondary stays SECONDARY.

**Resolution**:

1. **Grace-period caching** (ha-cluster.go):
   ```go
   type HA struct {
     lastElectionResult     *ElectionResult
     lastElectionTime       time.Time
     graceWindow            time.Duration  // 10 seconds default
   }

   func (h *HA) electPrimary(ctx context.Context) (*ElectionResult, error) {
     result, err := h.store.ElectionQuery(ctx)
     
     if err == DBPartitionError {
       now := time.Now()
       if h.lastElectionResult != nil && 
          now.Sub(h.lastElectionTime) < h.graceWindow {
         // DB partition but cache says we should be primary
         if h.lastElectionResult.Primary == h.serverID {
           h.transition(PRIMARY)
           h.metrics.incDBPartitionDetected()
           return h.lastElectionResult, nil
         }
       }
       // Grace window expired; enter ERROR
       h.transition(ERROR)
       return nil, err
     }
     
     h.lastElectionResult = result
     h.lastElectionTime = now
     return result, nil
   }
   ```

2. **Primary enters ERROR immediately on DB partition** (not after retries):
   - Heartbeat write fails once → transition to ERROR
   - Stop serving DHCP immediately

3. **Emit metric** `gdhcp_db_partition_detected` (counter) and **alert ops** to investigate network/DB health.

4. **Deploy guide documentation** (deploy-build.md §Troubleshooting):
   > "If both cluster servers show ERROR state, the database is unreachable from both. Check PostgreSQL connectivity, firewall rules, and network routing. Recovery: when DB recovers, restart gdhcp on primary server; secondary will detect election and rejoin."

---

### Major: RENEW Packet to Failed Primary

**Problem**: RENEW requests sent to old primary (identified by server-id option) are lost if primary down. Client waits until T2, then broadcasts REBIND.

**Resolution**:

1. **Secondary answers RENEW even if not original server**:
   ```go
   func (e *Engine) handleRENEW(ctx context.Context, pkt *DHCPPacket) error {
     // Extract requested IP (ciaddr) from packet
     requestedIP := net.IP(pkt.Packet.ClientIPAddress)
     
     // Look up lease (ignore server-id field; we accept renew from any server)
     lease, err := e.store.GetLeaseByIP(ctx, requestedIP)
     if err != nil || lease.Expired() {
       return e.sendNAK(pkt)
     }
     
     // Renew lease even if server-id doesn't match
     lease.LeaseEnd = time.Now().Add(e.config.LeaseTime)
     if err := e.store.UpdateLease(ctx, lease); err != nil {
       return e.sendNAK(pkt)
     }
     
     return e.sendACK(pkt, lease)
   }
   ```
   **Trade-off documented**: This deviates from strict RFC 2131 but is practical for HA. Secondary can renew any lease in the DB, not just those it allocated.

2. **Shorten T1 in HA mode** (recommended config):
   - Standard DHCP: T1 = 50% of lease time
   - HA mode: T1 = 40% of lease time
   - Gives more time to reach secondary before T2 (87.5%)
   - Config parameter: `ha_t1_percentage: 40` in config.yaml

3. **Custom backup server option** (future):
   - Non-standard DHCP option (code 253) to advertise backup server IP
   - Requires client support; document as optional feature
   - For now, document workaround: shorten T1

---

### Major: LISTEN/NOTIFY Connection Reliability

**Problem**: If LISTEN/NOTIFY connection drops, config notifications stop. Fallback polling (30s) too slow.

**Resolution**:

1. **LISTEN reconnect with backoff** (config-watcher.go):
   ```go
   func (cw *ConfigWatcher) watchConfigChanges(ctx context.Context) {
     backoff := time.Second
     maxBackoff := 30 * time.Second
     
     for {
       conn, err := cw.db.Conn(ctx)
       if err != nil {
         time.Sleep(backoff)
         backoff = time.Duration(float64(backoff) * 1.5)
         if backoff > maxBackoff {
           backoff = maxBackoff
         }
         continue
       }
       
       if err := conn.Raw(func(conn interface{}) error {
         return conn.(interface { Listen(context.Context, string) error }).Listen(ctx, "dhcp_config_change")
       }); err != nil {
         conn.Close()
         continue
       }
       
       // Listen successful; reset backoff
       backoff = time.Second
       
       // Handle notifications
       for {
         notification, err := conn.Notification(ctx)
         if err != nil {
           conn.Close()
           break  // Reconnect
         }
         cw.handleNotification(notification)
       }
     }
   }
   ```

2. **Polling interval reduced to 5 seconds in HA mode** (config.yaml):
   ```yaml
   config_check_interval_ms: 5000  # 5 seconds, down from 30 seconds
   ```

3. **Prometheus metric** `gdhcp_config_propagation_lag_ms` (histogram). Alert if > 5000ms.

4. **Log every LISTEN connection and disconnect** with timestamp (DEBUG level).

---

### Major: Initialization & Cluster Bootstrap

**Problem**: Bootstrap sequence underspecified. If both servers boot simultaneously, cluster_heartbeat is empty. What state do they enter?

**Resolution**:

1. **First-boot initialization** (ha-cluster.go `init()` function):
   ```go
   func (h *HA) initialize(ctx context.Context) error {
     // Check if cluster_heartbeat table is empty
     count, err := h.store.CountHeartbeats(ctx, h.clusterID)
     if err != nil {
       return err
     }
     
     if count == 0 {
       // Cluster is new; bootstrap it
       if h.nodeID == 1 {  // Lowest node_id
         h.store.InitializeHeartbeat(ctx, h.clusterID, h.serverID)
         log.Info("Cluster initialized; this server is primary (lowest node_id)")
         return nil
       }
       // Higher node_id; wait for primary to initialize
       time.Sleep(2 * time.Second)
       return h.initialize(ctx)  // Retry
     }
     return nil
   }
   ```

2. **Bootstrap CLI command** (optional, for pre-seeding):
   ```bash
   gdhcp-cli ha cluster bootstrap \
     --cluster-id prod-cluster \
     --server-1-id dhcp-01 \
     --server-2-id dhcp-02
   ```
   This pre-inserts heartbeat rows for both servers (avoids race on simultaneous boot).

3. **Documentation** (deploy-build.md §HA Deployment):
   > "Bootstrap sequence: The first server to connect to the database initializes the cluster heartbeat table. Other servers join automatically. Alternatively, use `gdhcp-cli ha cluster bootstrap ...` to pre-seed before starting servers."

4. **Metric** `gdhcp_cluster_init_detected` (counter) logged when cluster is first initialized.

---

### Major: Lease Allocation Retry on Error

**Problem**: `allocateLease()` failure returns error NAK, but if error is transient, NAK is incorrect.

**Resolution**:

1. **Retry loop in allocateLease** (allocation.go):
   - Already resolved in **Blocker: Lease Allocation Safety** section above
   - Retries up to 3 times on serialization errors with exponential backoff

2. **Distinguish pool exhaustion vs. serialization conflict**:
   ```go
   if err != nil {
     if isSerializationError(err) {
       // Transient; retry (already handled in retry loop)
       return nil, ErrAllocationRetry
     }
     if noRowsAffected {
       // Pool exhausted (no free IPs)
       return nil, ErrNoLeases  // Send NAK
     }
     // Other error
     return nil, err
   }
   ```

3. **Metric** `gdhcp_allocation_retries` (histogram) per allocation attempt. Alert if per-request average > 2.

4. **Documentation** (deployment guide):
   > "Concurrent clients benefit from slightly larger pools to reduce retry overhead. Recommended: pool size >= expected concurrent clients × 1.5 to minimize reallocation retries."

---

### Major: Deployment - Docker Network Mode

**Problem**: `network_mode: host` is critical for Docker but buried in comment. Naive deployment with bridge network breaks DHCP broadcast.

**Resolution**:

1. **Pre-startup socket check** (dhcp-engine.go):
   ```go
   func (e *Engine) verifyDHCPSocket(ctx context.Context) error {
     // After binding socket, try to receive a DISCOVER packet
     ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
     defer cancel()
     
     // Listen for any incoming DHCP packet
     pkt := make([]byte, 1024)
     n, _, err := e.conn.ReadFromUDP(pkt)
     if err == context.DeadlineExceeded && isDockerEnv() {
       log.Fatalf("DHCP socket bound but no packets received; ensure network_mode: host in docker-compose.yml")
     }
     if n > 0 {
       e.conn.ReadFromUDP(pkt)  // Consume packet; return to listening
     }
     return nil
   }
   ```

2. **Healthcheck in docker-compose.yml**:
   ```yaml
   healthcheck:
     test: ["CMD", "curl", "-f", "http://localhost:8080/api/v1/health"]
     interval: 30s
     timeout: 5s
     retries: 3
     start_period: 10s
   ```
   Plus a DHCP healthcheck (send DHCP DISCOVER, verify OFFER):
   ```yaml
   test: ["CMD", "gdhcp-cli", "dhcp", "health-check"]
   ```

3. **Prominent documentation** (deploy-build.md §Docker):
   > "**CRITICAL: Must use network_mode: host**. Container bridges and overlay networks will NOT work for DHCP broadcast. If you see 'no DHCP offers received' from clients, verify `network_mode: host` in docker-compose.yml."

---

### Major: Kubernetes hostNetwork and VIP

**Problem**: K8s manifests require `hostNetwork: true` but VIP failover is undocumented/unsupported. Naive operator expects automatic failover.

**Resolution**:

1. **Set `ha.enabled: false` in default K8s ConfigMap** (deploy/k8s/base/configmap.yaml):
   ```yaml
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: gdhcp-config
   data:
     config.yaml: |
       ha:
         enabled: false  # Disable HA by default in Kubernetes
   ```

2. **Prominent warning in deploy-build.md §Kubernetes**:
   > "HA VIP failover is NOT supported in Kubernetes. For redundancy, run 2+ replicas and use external load balancer (AWS NLB, GCP LB, Cloudflare) with health checks. To enable HA (experimental), set `ha.enabled: true` and provide a shared PostgreSQL instance; VIP failover will use netlink (hostNetwork required). This is not recommended for managed K8s clusters."

3. **Alternative: DaemonSet per node** (optional future):
   - One gdhcp replica per node (via DaemonSet)
   - VIP failover coordinated via DB
   - Requires extensive documentation; not in initial release

4. **Recommended K8s setup** (documented with example manifests):
   - Deployment (not DaemonSet) with 2–3 replicas
   - External load balancer with DHCP health check
   - gdhcp configured with `ha.enabled: false`
   - Load balancer distributes DHCP requests across replicas

---

### Major: Concurrent DHCP Load-Sharing

**Problem**: Both servers answer DISCOVER, sending duplicate OFFERs. Impact on clients not documented.

**Resolution**:

1. **Document duplicate OFFER behavior** (ha-cluster.md §Concurrent DHCP Serving):
   > "Both primary and secondary servers answer DISCOVER broadcasts concurrently. Clients receive 2 OFFERs with identical IP and options (since lease allocation is atomic in DB). This is compliant with DHCP spec (RFC 2131 §3.1.1); clients select one OFFER and send REQUEST. Duplicate OFFERs are harmless and reduce failover latency. Network overhead: ~100 bytes per OFFER × client count per cycle; negligible on most networks."

2. **No duplicate suppression by default** (simplifies design).

3. **Optional future**: server ID-based duplicate suppression (even/odd XID allocation) if needed.

---

### Major: Kubernetes Pod Scheduling (One Pod Per Node)

**Problem**: Multiple gdhcp pods on same node conflict on port 67.

**Resolution**:

1. **Pod anti-affinity rule** (deploy/k8s/base/deployment.yaml):
   ```yaml
   affinity:
     podAntiAffinity:
       requiredDuringSchedulingIgnoredDuringExecution:
       - labelSelector:
           matchExpressions:
           - key: app
             operator: In
             values:
             - gdhcp
         topologyKey: kubernetes.io/hostname
   ```

2. **Documentation** (deploy-build.md):
   > "Each Kubernetes node can run at most one gdhcp pod (port 67 conflict). Use pod anti-affinity to enforce one replica per node. For 3-node cluster, set `replicas: 3`."

---

### Minor: Capabilities & Security

**Problem**: Install script grants capabilities but doesn't verify. Silent failures if misconfigured.

**Resolution**:

1. **Startup capability check** (server/cmd/gdhcp/main.go):
   ```go
   func verifyCapabilities() error {
     // Test raw ICMP socket (requires CAP_NET_RAW)
     conn, err := net.ListenIP("ip4:icmp", &net.IPAddr{IP: net.IP{127, 0, 0, 1}})
     if err != nil {
       return fmt.Errorf("CAP_NET_RAW not set: %w. Run: sudo setcap cap_net_raw=ep /usr/local/bin/gdhcp", err)
     }
     conn.Close()
     
     // Test netlink socket (requires CAP_NET_ADMIN)
     // (via vishvananda/netlink, try to list interfaces)
     if err := testNetlinkAccess(); err != nil {
       return fmt.Errorf("CAP_NET_ADMIN not set: %w. Run: sudo setcap cap_net_admin=ep /usr/local/bin/gdhcp", err)
     }
     
     return nil
   }
   
   func main() {
     if config.HA.Enabled {
       if err := verifyCapabilities(); err != nil {
         log.Fatalf("Capability check failed: %v", err)
       }
     }
     // Continue startup
   }
   ```

2. **Document in install.sh comments**:
   ```bash
   # Grant capabilities for HA VIP failover (netlink, raw sockets)
   if [ "$HA_ENABLED" = "true" ]; then
     echo "Setting Linux capabilities for HA VIP management..."
     sudo setcap cap_net_admin=ep /usr/local/bin/gdhcp
     sudo setcap cap_net_raw=ep /usr/local/bin/gdhcp
   fi
   ```

---

### Minor: Election Query Tie-Breaking

**Problem**: Tie-breaker uses `created_at` which can collide at sub-millisecond precision or drift on NTP adjustment.

**Resolution**:

1. **Add server UUID as final tie-breaker** (ha-cluster.go):
   ```sql
   WITH ranked_servers AS (
     SELECT
       ch.server_id,
       cs.node_id,
       ch.heartbeat_time,
       cs.id,  -- UUID primary key
       ROW_NUMBER() OVER (
         ORDER BY 
           cs.node_id ASC, 
           ch.heartbeat_time DESC, 
           cs.created_at ASC,
           cs.id ASC  -- Final tie-breaker: UUID (immutable, unique)
       ) AS rank
     FROM cluster_heartbeat ch
     JOIN cluster_servers cs ON ch.server_id = cs.id
     WHERE ch.cluster_id = $1
       AND ch.heartbeat_time > now() - interval '1 millisecond' * $2
   )
   SELECT server_id FROM ranked_servers WHERE rank = 1;
   ```

2. **Validate node_id uniqueness** (at cluster config load):
   ```go
   func (h *HA) validateConfig() error {
     serverIDs := make(map[int]bool)
     for _, srv := range h.servers {
       if serverIDs[srv.NodeID] {
         return fmt.Errorf("duplicate node_id: %d", srv.NodeID)
       }
       serverIDs[srv.NodeID] = true
     }
     return nil
   }
   ```

3. **Documentation**: "node_ids must be unique per cluster; election is deterministic."

---

### Minor: Metrics & Monitoring Gaps

**Problem**: Missing metrics for heartbeat latency, election duration, VIP assignment.

**Resolution**:

1. **Add Prometheus metrics** (metrics/metrics.go):
   - `gdhcp_heartbeat_write_duration_ms` (histogram, 10/50/100/500/1000ms buckets)
   - `gdhcp_election_query_duration_ms` (histogram)
   - `gdhcp_vip_assignment_duration_ms` (histogram)
   - `gdhcp_gratuitous_arp_failures_total` (counter)
   - `gdhcp_config_propagation_lag_ms` (histogram)
   - `gdhcp_config_reload_duration_ms` (histogram)
   - `gdhcp_allocation_retries` (histogram)
   - `gdhcp_db_partition_detected` (counter)
   - `gdhcp_election_query_timeouts_total` (counter)

2. **Alert thresholds** (in Prometheus alerting rules or Grafana):
   - WARN: heartbeat_write_duration > 100ms
   - ALERT: heartbeat_write_duration > 500ms for 5 minutes
   - WARN: election_query_duration > 100ms
   - ALERT: election_query_duration > 500ms
   - ALERT: vip_assignment_failures > 0 (any failure)
   - ALERT: config_propagation_lag > 5000ms

3. **Tracing** (OpenTelemetry, optional):
   - Instrument ElectionQuery, VIPAssignment, GratuitousARP with spans
   - Export to Jaeger or Honeycomb for debugging failover critical path

4. **Documentation** (deploy-build.md §Monitoring):
   > "Expected latencies:
   > - Heartbeat write: <50ms typical; alert if > 200ms for 5 min
   > - Election query: <50ms typical; alert if > 500ms for 5 min
   > - VIP assignment: <100ms typical; alert if any failure"

---

### Minor: DHCP Option Inheritance & Caching

**Problem**: Option resolution queries DB 4–5 times per DHCP request. Under load, DB overload.

**Resolution**:

1. **Option caching** (config-manager.go):
   ```go
   type OptionCache struct {
     cache sync.Map  // map[(scopeID, poolID)] -> resolvedOptions
     ttl   time.Duration
   }

   func (oc *OptionCache) GetOptions(scopeID, poolID string) ([]*DHCPOption, error) {
     if cached, ok := oc.cache.Load((scopeID, poolID)); ok && !expired(cached) {
       return cached.(*CachedOptions).Options, nil
     }
     
     // Miss: query DB
     opts, err := oc.store.ResolveOptions(scopeID, poolID)
     if err == nil {
       oc.cache.Store((scopeID, poolID), &CachedOptions{
         Options:  opts,
         CachedAt: time.Now(),
       })
     }
     return opts, err
   }

   func (oc *OptionCache) InvalidatePool(poolID string) {
     oc.cache.Range(func(key, val interface{}) bool {
       if k := key.([2]string); k[1] == poolID {
         oc.cache.Delete(key)
       }
       return true
     })
   }
   ```
   - TTL: 60 seconds default (configurable)
   - Invalidate cache on API write to options/pools

2. **Cache metrics** (metrics.go):
   - `gdhcp_option_cache_hits_total` (counter)
   - `gdhcp_option_cache_misses_total` (counter)
   - `gdhcp_option_cache_size_bytes` (gauge)

3. **Lazy-load options** (in DHCP handler):
   - Only query for options in client's Parameter Request List (PRL)
   - Don't fetch all 254 options; fetch only what client asks for

4. **Batch queries** (option-store.go):
   - If handling 10 clients from same scope, batch: `SELECT ... WHERE scope_id IN (...)`

---

### Minor: Configuration Validation Missing

**Problem**: Heartbeat config constraints not validated (ttl >= 2× interval). Invalid values cause unpredictable behavior.

**Resolution**:

1. **Validation at config load** (config/loader.go):
   ```go
   func (cfg *HAConfig) Validate() error {
     if cfg.HeartbeatTTLMs < 2*cfg.HeartbeatIntervalMs {
       return fmt.Errorf(
         "invalid HA config: heartbeat_ttl_ms (%d) must be >= 2 * heartbeat_interval_ms (%d)",
         cfg.HeartbeatTTLMs, cfg.HeartbeatIntervalMs,
       )
     }
     if cfg.ElectionIntervalMs > cfg.HeartbeatTTLMs/2 {
       return fmt.Errorf(
         "election_interval_ms (%d) should be <= heartbeat_ttl_ms / 2 (%d) for prompt detection",
         cfg.ElectionIntervalMs, cfg.HeartbeatTTLMs/2,
       )
     }
     return nil
   }
   ```

2. **Log all config values at startup** (main.go):
   ```go
   log.Infof("HA cluster config: heartbeat_interval=%dms, heartbeat_ttl=%dms, election_interval=%dms",
     cfg.HeartbeatIntervalMs, cfg.HeartbeatTTLMs, cfg.ElectionIntervalMs)
   ```

3. **API validation** (api/handler.go):
   ```go
   func (h *Handler) UpdateClusterConfig(w http.ResponseWriter, r *http.Request) {
     var cfg ClusterConfig
     json.NewDecoder(r.Body).Decode(&cfg)
     
     if err := cfg.Validate(); err != nil {
       http.Error(w, fmt.Sprintf("Invalid config: %v", err), http.StatusBadRequest)
       return
     }
     // ... persist
   }
   ```

---

### Blocker: Data Model - VRF Overlapping CIDR Constraint

**Problem**: UNIQUE INDEX on (vrf_id, prefix) only prevents exact duplicates, not overlaps (10.0.0.0/16 and 10.0.0.0/17).

**Resolution**:

Replace UNIQUE INDEX with PostgreSQL EXCLUSION constraint (using GIST):

```sql
-- Enable btree_gist extension
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Replace UNIQUE INDEX with EXCLUSION constraint
ALTER TABLE ipam_prefixes DROP CONSTRAINT idx_ipam_prefixes_no_overlap;

ALTER TABLE ipam_prefixes ADD CONSTRAINT no_overlapping_subnets EXCLUDE USING GIST (
  vrf_id WITH =,
  prefix WITH &&
) WHERE (prefix_type = 'subnet');
```

This enforces no overlapping CIDRs within a VRF for subnet-type prefixes, while allowing overlap across VRFs and between aggregates/containers and subnets.

---

### Blocker: Data Model - Pool Range Constraints via Trigger

**Problem**: CHECK constraints with subqueries in `dhcp_pools` table are unreliable on concurrent updates.

**Resolution**:

Replace CHECK constraints with trigger-based validation (pool-constraints-trigger.sql):

```sql
CREATE FUNCTION validate_pool_range()
RETURNS TRIGGER AS $$
BEGIN
  -- Fetch scope's network
  DECLARE
    scope_network CIDR;
  BEGIN
    SELECT network INTO scope_network FROM dhcp_scopes WHERE id = NEW.scope_id;
    
    IF scope_network IS NULL THEN
      RAISE EXCEPTION 'Scope % not found', NEW.scope_id;
    END IF;
    
    -- Validate range_start and range_end are within scope
    IF NOT (NEW.range_start::cidr << scope_network AND 
            NEW.range_end::cidr << scope_network) THEN
      RAISE EXCEPTION 'Pool range [%, %] outside scope %', 
        NEW.range_start, NEW.range_end, scope_network;
    END IF;
    
    IF NEW.range_start > NEW.range_end THEN
      RAISE EXCEPTION 'Pool range_start > range_end';
    END IF;
    
    RETURN NEW;
  END;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_validate_pool_range
BEFORE INSERT OR UPDATE ON dhcp_pools
FOR EACH ROW
EXECUTE FUNCTION validate_pool_range();
```

Triggers execute within transaction context, providing reliable validation on concurrent updates.

---

### Major: Data Model - Utilization Query Performance

**Problem**: Live utilization query (full join on pools/leases) scans millions of rows. Dashboard queries timeout.

**Resolution**:

1. **Separate concerns**: Use `stats_utilization_samples` for dashboard (historical trends), cached active lease count for current utilization.

2. **Add `stats_active_lease_count` table** (computed incrementally):
   ```sql
   CREATE TABLE stats_active_lease_count (
     id BIGSERIAL PRIMARY KEY,
     pool_id UUID NOT NULL UNIQUE REFERENCES dhcp_pools(id),
     active_count BIGINT NOT NULL DEFAULT 0,
     updated_at TIMESTAMP DEFAULT NOW(),
     UNIQUE(pool_id)
   );

   CREATE INDEX idx_active_count_pool ON stats_active_lease_count(pool_id);
   ```

3. **Update incrementally** (on lease allocation/expiry):
   ```sql
   -- After allocating a lease
   INSERT INTO stats_active_lease_count (pool_id, active_count)
   VALUES ($pool_id, 1)
   ON CONFLICT (pool_id) DO UPDATE
   SET active_count = active_count + 1, updated_at = NOW();
   ```

4. **Current utilization query** (O(pools), not O(leases)):
   ```sql
   SELECT
     scope_id,
     SUM(active_count) as total_active,
     COALESCE(SUM(range_end - range_start + 1), 0) as total_ips,
     100.0 * SUM(active_count) / COALESCE(SUM(range_end - range_start + 1), 1) as utilization_pct
   FROM dhcp_pools
   LEFT JOIN stats_active_lease_count ON dhcp_pools.id = pool_id
   WHERE scope_id = $scope_id
   GROUP BY scope_id;
   ```

---

### Major: Data Model - Lease Table Bloat

**Problem**: No purging strategy. Unbounded table growth causes full table scans, index bloat.

**Resolution**:

1. **Define retention policy** (in config.yaml):
   ```yaml
   database:
     lease_retention_days: 90  # Keep non-active leases for 90 days
   ```

2. **Background cleanup job** (internal/jobs/lease-cleanup.go):
   ```go
   func (lc *LeaseCleanupJob) Run(ctx context.Context) error {
     cutoff := time.Now().Add(-time.Duration(lc.retentionDays) * 24 * time.Hour)
     
     result, err := lc.db.ExecContext(ctx, `
       DELETE FROM dhcp_leases
       WHERE binding_state IN ('released', 'expired')
         AND lease_end < $1
     `, cutoff)
     
     if err != nil {
       return err
     }
     
     log.Infof("Deleted %d expired leases", result.RowsAffected())
     return nil
   }
   ```

3. **Partition strategy** (optional, for very large deployments):
   ```sql
   CREATE TABLE dhcp_leases_2026_01 PARTITION OF dhcp_leases
   FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
   ```
   Enables efficient deletion of entire month partitions without DELETE scanning.

4. **Documentation** (deploy-build.md §Operations):
   > "Lease retention: non-active leases retained for 90 days (configurable), then purged. For audit compliance, archive old leases to separate archive table before deletion. Partition the lease table by month to enable efficient cleanup."

---

### Major: API Design - Search Performance

**Problem**: Global search uses case-insensitive substring match without index. Full table scans on every search.

**Resolution**:

1. **Commit to PostgreSQL trigram index** (schema.sql):
   ```sql
   CREATE EXTENSION IF NOT EXISTS pg_trgm;
   
   CREATE INDEX idx_ipam_prefixes_name_trgm 
   ON ipam_prefixes USING GIN(name gin_trgm_ops);
   
   CREATE INDEX idx_dhcp_scopes_name_trgm 
   ON dhcp_scopes USING GIN(name gin_trgm_ops);
   
   -- Similar for other searchable tables
   ```

2. **Search query** (api/search.go):
   ```go
   func (s *SearchHandler) Search(ctx context.Context, query string) ([]SearchResult, error) {
     pattern := query + ":*"  // Trigram pattern
     
     var results []SearchResult
     rows, _ := s.db.QueryContext(ctx, `
       SELECT 'ipam_prefix' as entity_type, id, name, description
       FROM ipam_prefixes
       WHERE name % $1  -- ILIKE operator with trigram index
       ORDER BY similarity(name, $1) DESC
       LIMIT 100
     `, query)
     
     // Process rows, append to results
     // Similar queries for dhcp_scopes, dhcp_pools, etc.
     
     return results, nil
   }
   ```

3. **Performance**: Trigram index makes search O(log n) + O(result count). Acceptable for real-time search.

4. **Documentation** (api-cli.md):
   > "Search: Uses PostgreSQL trigram index for fast substring matching. Case-insensitive. Example: search 'prod' returns all entities with 'prod' in name/description. Maximum 100 results per entity type."

---

### Major: API Design - Pagination Strategy

**Problem**: Offset-based pagination is O(offset); deep pagination slow. No cursor-based alternative.

**Resolution**:

1. **Support both offset and cursor pagination** (api/list.go):
   ```go
   type ListQuery struct {
     Limit  int    `query:"limit" default:"100" max:"1000"`
     Offset int    `query:"offset" default:"0"`
     Cursor string `query:"cursor"`  // Opaque token
   }

   func (h *Handler) ListLeases(w http.ResponseWriter, r *http.Request) {
     query := parseListQuery(r)
     
     if query.Cursor != "" {
       // Cursor-based pagination
       cursor, _ := decodeCursor(query.Cursor)
       results, nextCursor := h.db.GetLeasesAfter(ctx, cursor, query.Limit)
       respondJSON(w, ListResponse{
         Items:      results,
         NextCursor: nextCursor,
         HasMore:    nextCursor != "",
       })
     } else {
       // Offset pagination (backward compatibility)
       total, _ := h.db.CountLeases(ctx)
       results, _ := h.db.GetLeasesOffset(ctx, query.Offset, query.Limit)
       respondJSON(w, ListResponse{
         Items:  results,
         Total:  total,
         Offset: query.Offset,
         Limit:  query.Limit,
       })
     }
   }
   ```

2. **Cursor encoding** (keyset pagination):
   ```go
   type Cursor struct {
     LastID        string    // lease ID
     LastTimestamp time.Time // sort key
   }
   
   func encodeCursor(cursor Cursor) string {
     // base64(lastID + "|" + lastTimestamp)
     return base64.StdEncoding.EncodeToString(
       []byte(fmt.Sprintf("%s|%d", cursor.LastID, cursor.LastTimestamp.Unix())),
     )
   }
   ```

3. **Query with cursor**:
   ```sql
   SELECT * FROM dhcp_leases
   WHERE (lease_start > $1 OR (lease_start = $1 AND id > $2))
   ORDER BY lease_start DESC, id DESC
   LIMIT 101;  -- +1 to detect has_more
   ```

4. **Documentation** (api-cli.md):
   > "Pagination: Use cursor pagination for large datasets (fast, O(1) per page). Use offset pagination for small datasets (<100k rows). Cursor format is opaque; don't decode."

---

### Minor: API Design - Options Endpoint Consistency

**Problem**: GET /api/v1/options and GET /api/v1/options/catalog have different schemas.

**Resolution**:

1. **Unified schema** (api/models.go):
   ```go
   type DHCPOption struct {
     ID          string `json:"id"`
     Code        int16  `json:"code"`
     Name        string `json:"name"`
     Description string `json:"description"`
     DataType    string `json:"data_type"` // 'string', 'ip', 'ip_list', etc.
     IsStandard  bool   `json:"is_standard"` // From IANA catalog
     IsCustom    bool   `json:"is_custom"`   // User-defined
     CreatedAt   time.Time `json:"created_at"`
   }
   ```

2. **Endpoint consolidation**:
   ```
   GET /api/v1/options?standard=true   # IANA options only
   GET /api/v1/options?custom=true     # User-defined only
   GET /api/v1/options?standard=true&custom=true  # All
   ```

3. **Response**:
   ```json
   {
     "items": [
       {"code": 6, "name": "domain_name_servers", "is_standard": true, ...},
       {"code": 253, "name": "custom_foo", "is_custom": true, ...}
     ]
   }
   ```

---

### Blocker: Build System - go:embed Fragility

**Problem**: go:embed paths only work from specific build directory. High risk of path breakage.

**Resolution**:

1. **Document absolute requirement** (Makefile comment):
   ```makefile
   # IMPORTANT: Build from repo root only
   # go:embed uses relative paths from the file's location, not CWD
   # Incorrect: cd server && go build ./cmd/gdhcp  (embed paths will break)
   # Correct: go build ./server/cmd/gdhcp           (from repo root)
   ```

2. **Validate embed paths** (main.go):
   ```go
   //go:embed ../../migrations/*.sql
   var Migrations embed.FS

   func init() {
     // Verify migrations are embedded
     entries, err := Migrations.ReadDir("../../migrations")
     if err != nil || len(entries) == 0 {
       log.Fatalf("No migrations embedded; ensure build from repo root")
     }
   }
   ```

3. **CI/CD safety** (GitHub Actions workflow):
   ```yaml
   - name: Build
     working-directory: <repo>
     run: make all  # Always from repo root
   ```

---

### Blocker: DHCP Engine - UDP Broadcast Socket

**Problem**: Standard 0.0.0.0:67 binding doesn't reliably send DHCP broadcast responses.

**Resolution**:

**Chosen strategy**: Per-interface UDP listeners with SO_BINDTODEVICE (Linux-specific, most portable for production).

```go
// internal/dhcp/listener.go
func (il *InterfaceListener) bindSocket() error {
  addr := net.UDPAddr{Port: 67}
  conn, err := net.ListenUDP("udp4", &addr)
  if err != nil {
    return err
  }

  // Enable broadcast
  rawConn, _ := conn.SyscallConn()
  rawConn.Control(func(fd uintptr) {
    syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
    // Bind to specific interface (Linux)
    syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, il.ifname)
  })

  il.conn = conn
  return nil
}

// Response sending: use UDP directly (not raw sockets)
func (il *InterfaceListener) sendOffer(pkt *DHCPPacket, offer *dhcpv4.DHCPv4) error {
  // Unicast to relay giaddr or broadcast to 255.255.255.255
  destAddr := &net.UDPAddr{IP: net.IP{255, 255, 255, 255}, Port: 68}
  if pkt.Packet.GatewayIPAddress != nil {
    destAddr.IP = net.IP(pkt.Packet.GatewayIPAddress)
  }

  pktBytes, _ := offer.Marshal()
  _, err := il.conn.WriteToUDP(pktBytes, destAddr)
  return err
}
```

**Rationale**: UDP with SO_BROADCAST is portable across Linux/FreeBSD/macOS, simpler than raw sockets, and sufficient for DHCP protocol.

---

### Blocker: DHCP Engine - Relay Agent Support

**Problem**: FindScopeByGiaddr method undefined.

**Resolution**:

Add to ConfigStore interface (internal/store/interface.go):

```go
type ConfigStore interface {
  // ... existing methods ...

  // FindScopeByGiaddr returns the DHCP scope that should serve a relayed request
  // based on the relay agent's gateway IP address (giaddr field).
  // The giaddr is looked up against all known scope subnets to find a match.
  FindScopeByGiaddr(ctx context.Context, giaddr net.IP) (*Scope, error)
}
```

Implementation (internal/store/postgres.go):

```go
func (ps *PostgresStore) FindScopeByGiaddr(ctx context.Context, giaddr net.IP) (*Scope, error) {
  // Find scope whose network contains this giaddr
  var scope Scope
  err := ps.db.QueryRowContext(ctx, `
    SELECT ds.id, ds.name, ds.vrf_id, ds.network
    FROM dhcp_scopes ds
    WHERE ds.network >> $1  -- CIDR contains operator
    LIMIT 1
  `, giaddr.String()).Scan(&scope.ID, &scope.Name, &scope.VRFID, &scope.Network)

  if err != nil {
    return nil, fmt.Errorf("no scope found for giaddr %s: %w", giaddr, err)
  }

  return &scope, nil
}
```

---

### Blocker: HA Cluster - Netlink Library Specification

**Problem**: Netlink library choice undefined.

**Resolution**:

**Chosen library**: `github.com/vishvananda/netlink` v1.2.0+

Rationale:
- Widely used in production (Kubernetes uses it)
- Supports IPv4/IPv6 address management
- Provides LinkByName, AddrAdd, AddrReplace, AddrDel primitives needed for VIP assignment
- Active maintenance

Usage (internal/ha/vip.go):

```go
import "github.com/vishvananda/netlink"

func (h *HA) assignVIP(vipAddr net.IPNet, iface string) error {
  link, err := netlink.LinkByName(iface)
  if err != nil {
    return fmt.Errorf("interface %s not found: %w", iface, err)
  }

  addr := &netlink.Addr{
    IPNet: &vipAddr,
    Label: "",
  }

  // Use AddrReplace for idempotency (no error if already assigned)
  if err := netlink.AddrReplace(link, addr); err != nil {
    return fmt.Errorf("failed to assign VIP: %w", err)
  }

  return nil
}
```

---

### Blocker: HA Cluster - Gratuitous ARP Implementation

**Problem**: Gratuitous ARP only pseudocode; no concrete implementation.

**Resolution**:

**Chosen library**: `github.com/mdlayler/arp` v1.5.0+

Implementation (internal/ha/arp.go):

```go
import (
  "github.com/mdlayler/arp"
  "github.com/vishvananda/netlink"
)

func (h *HA) sendGratuitousARP(vipAddr net.IP, iface string) error {
  link, _ := netlink.LinkByName(iface)
  addrs, _ := netlink.AddrList(link, netlink.FAMILY_V4)

  if len(addrs) == 0 {
    return errors.New("no IPv4 address on interface")
  }

  srcIP := addrs[0].IP

  // Create ARP request (gratuitous: sender IP == target IP)
  arp := &arp.Packet{
    HardwareType: arp.HardwareEthernet,
    ProtocolType: arp.ProtocolIPv4,
    HardwareLen:  6,
    ProtocolLen:  4,
    Operation:    arp.OperationRequest,
    SenderHardwareAddr: link.Attrs().HardwareAddr,
    SenderProtocolAddr: vipAddr.To4(),
    TargetHardwareAddr: net.HardwareAddr{0, 0, 0, 0, 0, 0},
    TargetProtocolAddr: vipAddr.To4(),
  }

  packet, _ := arp.Marshal()

  // Send on interface (raw packet, layer 2)
  conn, _ := arp.Dial(link)
  defer conn.Close()

  _, err := conn.WriteTo(packet, &net.UDPAddr{})
  return err
}
```

Retry logic (in ha-cluster.go):

```go
func (h *HA) ensureGratuitousARPSent() {
  for i := 0; i < 3; i++ {
    if err := h.sendGratuitousARP(h.vipAddr, h.vipIface); err == nil {
      return
    }
    if i < 2 {
      time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
    }
  }
  h.metrics.incGratuitousARPFailures()
}
```

---

### Blocker: Database - Schema Migration Locking

**Problem**: Multiple servers starting simultaneously can race on migrations.

**Resolution**:

PostgreSQL advisory locks serialize migrations automatically. Ensure migration runner uses explicit locking:

```go
// internal/db/migrate.go
func (m *Migrator) Run(ctx context.Context) error {
  conn, _ := m.db.Conn(ctx)
  defer conn.Close()

  // Acquire advisory lock (16 bits = database lock ID)
  err := conn.Raw(func(conn interface{}) error {
    _, err := conn.(interface{ ExecContext(context.Context, string, ...interface{}) (interface{}, error) }).
      ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", 12345)
    return err
  })

  if err != nil {
    return fmt.Errorf("failed to acquire migration lock: %w", err)
  }

  // Lock held for duration of transaction; run migrations
  return m.executeMigrations(ctx)
}
```

PostgreSQL will serialize all advisory locks, ensuring only one migration runner at a time.

---

### Blocker: HA Cluster - Cluster Membership Initialization

**Problem**: Servers must be pre-registered in cluster_servers; no self-registration or discovery process.

**Resolution**:

**Support both manual registration and self-registration**:

1. **Manual pre-registration** (recommended for production):
   ```sql
   INSERT INTO cluster_servers (cluster_id, node_name, node_id, api_address, dhcp_address)
   VALUES (
     'cluster-uuid',
     'dhcp-server-1',
     1,
     '192.168.1.10:8080',
     '0.0.0.0:67'
   );
   ```

2. **Self-registration on first boot** (fallback):
   ```go
   func (h *HA) selfRegister(ctx context.Context) error {
     if h.serverID == "" {
       h.serverID = os.Hostname()  // Or env var GDHCP_SERVER_ID
     }
     
     // Check if already registered
     exists, _ := h.store.ServerExists(ctx, h.clusterID, h.serverID)
     if exists {
       return nil
     }
     
     // Register self
     nodeID, _ := h.getNextNodeID(ctx)  // Sequential assignment
     return h.store.RegisterServer(ctx, &Server{
       ClusterID:  h.clusterID,
       NodeName:   h.serverID,
       NodeID:     nodeID,
       APIAddr:    h.config.APIAddr,
       DHCPAddr:   h.config.DHCPAddr,
     })
   }
   ```

3. **Cluster bootstrap CLI** (optional):
   ```bash
   gdhcp-cli ha cluster bootstrap \
     --cluster-id prod-cluster \
     --server1-id dhcp-01 \
     --server2-id dhcp-02
   ```

**Documentation** (deploy-build.md):
> "Cluster membership: Servers are registered in the database before starting. Use manual SQL INSERT (recommended) or automatic self-registration on first boot (set env var `GDHCP_SERVER_ID` and `GDHCP_CLUSTER_ID`). Verify with: `gdhcp-cli ha cluster status --cluster-id <id>`."

---

### Blocker: Build/Deploy - main.go Missing Import

**Problem**: main.go references os.Stderr/os.Exit but no "os" import.

**Resolution**:

Corrected main.go (server/cmd/gdhcp/main.go):

```go
package main

import (
  "context"
  "fmt"
  "os"
  "os/signal"
  "syscall"

  "github.com/shamalawy/gdhcp/internal/server"
)

//go:embed ../../migrations/*.sql
var Migrations embed.FS

//go:embed ../../../web/dist/*
var WebAssets embed.FS

func main() {
  app := server.New(
    server.WithMigrations(Migrations),
    server.WithWebAssets(WebAssets),
  )

  if err := app.Start(context.Background()); err != nil {
    fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
    os.Exit(1)
  }

  // Wait for SIGTERM
  sigChan := make(chan os.Signal, 1)
  signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
  <-sigChan

  if err := app.Stop(); err != nil {
    fmt.Fprintf(os.Stderr, "Failed to stop server: %v\n", err)
    os.Exit(1)
  }
}
```

---

### Major: Deployment - Kubernetes ConfigMap Substitution

**Problem**: ConfigMap shows variable substitution (${GDHCP_DB_DSN}) but ConfigMaps don't perform substitution.

**Resolution**:

Use environment variables with `envFrom`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gdhcp-config
data:
  ha.enabled: "true"
  ha.heartbeat_interval_ms: "1000"
---
apiVersion: v1
kind: Secret
metadata:
  name: gdhcp-secrets
type: Opaque
data:
  db-dsn: cG9zdGdyZXM6Ly91c2VyOnBhc3NAcG9zdGdyZXM6NTQzMi9nZGhjcA==  # base64
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gdhcp
spec:
  template:
    spec:
      containers:
      - name: gdhcp
        image: gdhcp:latest
        envFrom:
        - configMapRef:
            name: gdhcp-config
        - secretRef:
            name: gdhcp-secrets
        env:
        - name: GDHCP_DB_DSN
          valueFrom:
            secretKeyRef:
              name: gdhcp-secrets
              key: db-dsn
```

gdhcp binary reads from environment (via `viper` or `flag` package):

```go
func LoadConfig() *Config {
  return &Config{
    Database: Config{
      DSN: os.Getenv("GDHCP_DB_DSN"),
    },
    HA: HAConfig{
      Enabled: os.Getenv("GDHCP_HA_ENABLED") == "true",
    },
  }
}
```

---

### Major: HA Cluster - VIP Assignment Race

**Problem**: New primary assigns VIP while old primary still holds it.

**Resolution**:

Already covered in **Blocker: VIP Assignment** section. Use netlink.AddrReplace() for idempotency and add 50–100ms jitter to old primary's VIP removal.

---

### Major: HA Cluster - State Machine Concurrency

**Problem**: State transitions not protected; no mutex/queue ensures atomicity.

**Resolution**:

```go
type HA struct {
  stateMu       sync.Mutex
  currentRole   string  // PRIMARY, SECONDARY, ERROR, etc.
  lastRoleChange time.Time
}

func (h *HA) transition(newRole string) {
  h.stateMu.Lock()
  defer h.stateMu.Unlock()

  if h.currentRole == newRole {
    return  // No change
  }

  log.Infof("Role transition: %s -> %s", h.currentRole, newRole)
  h.currentRole = newRole
  h.lastRoleChange = time.Now()

  // Trigger side effects based on new role
  h.onRoleChange(newRole)
}

func (h *HA) getRole() string {
  h.stateMu.RLock()
  defer h.stateMu.RUnlock()
  return h.currentRole
}
```

---

### Major: DHCP Engine - Concurrent Lease Checks

**Problem**: Lease existence checked outside transaction; races with concurrent allocations or expiry reaper.

**Resolution**:

Move all lease checks inside SERIALIZABLE transaction (allocation.go):

```go
func (e *Engine) allocateLease(...) (*Lease, error) {
  txn, _ := e.db.BeginTx(ctx, &sql.TxOptions{
    Isolation: sql.LevelSerializable,
  })
  defer txn.Rollback()

  // ENTIRE allocation inside transaction with SERIALIZABLE isolation
  existing, _ := txn.GetLeaseByClient(ctx, clientMAC, clientID)
  freeIP, _ := txn.SelectFreeIP(ctx, poolID)
  result, _ := txn.ExecContext(ctx, updateLeaseQuery, ...)

  txn.Commit()
  return lease, nil
}
```

---

### Major: Build System - CGO Requirement

**Problem**: CGO_ENABLED=1 required; blocks cross-compilation; increases Docker image size.

**Resolution**:

CGO is necessary for PostgreSQL driver (pq or pgx require libpq). Accept this requirement.

**Docker Dockerfile optimization**:

```dockerfile
# Multi-stage build
FROM golang:1.26-alpine AS builder
RUN apk add gcc musl-dev postgresql-dev
WORKDIR /build
COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o gdhcp ./server/cmd/gdhcp

# Final stage: lean runtime
FROM alpine:latest
RUN apk add --no-cache ca-certificates libpq
COPY --from=builder /build/gdhcp /usr/local/bin/gdhcp
EXPOSE 67/udp 8080
ENTRYPOINT ["gdhcp"]
```

This keeps final image small (~50MB) despite CGO requirement.

---

## VRF & Option Inheritance Semantics

### VRF Isolation

Each VRF is a separate DHCP domain with independent IPAM space:

- **Same VRF**: CIDR overlap forbidden at subnet level (via EXCLUSION constraint)
- **Different VRFs**: Same CIDR allowed (e.g., 10.0.0.0/24 in VRF-A and VRF-B)
- **Scope binding**: Each scope bound to single VRF; serves only clients in that VRF

### Option Inheritance Chain

**Precedence (highest → lowest)**:

1. **Reservation option** (dhcp_option_values with scope_level='reservation')
2. **Pool option**
3. **Scope option**
4. **Template option** (if multiple templates, use highest priority)
5. **Global option**

**Resolution algorithm** (dhcp-engine.go):

```go
func (e *Engine) resolveOption(ctx context.Context, optCode int16, 
  scope, pool, reservation *Entity) (*DHCPOption, error) {
  
  levels := []string{"reservation", "pool", "scope", "template", "global"}
  
  for _, level := range levels {
    opt, _ := e.store.GetOption(ctx, optCode, level, entityID(level))
    if opt != nil && opt.IsEnabled {
      return opt, nil
    }
  }
  
  return nil, ErrOptionNotFound
}
```

**Template priority**: If scope linked to multiple templates via dhcp_template_scopes (with priority field), check highest-priority template first.

---

## Configuration File Schema

**Path**: `/etc/gdhcp/config.yaml` (or `./config.yaml` for Docker/K8s)

```yaml
---
server:
  # HTTP API
  api_addr: "0.0.0.0:8080"
  api_tls_enabled: false
  api_tls_cert: "/etc/gdhcp/certs/server.crt"
  api_tls_key: "/etc/gdhcp/certs/server.key"

database:
  # PostgreSQL connection
  dsn: "postgres://gdhcp_user:password@db.example.com:5432/gdhcp"
  max_open_conns: 25
  max_idle_conns: 5
  conn_max_lifetime: "5m"
  
  # Lease retention
  lease_retention_days: 90
  
  # Option caching
  option_cache_ttl: "60s"

dhcp:
  # DHCP server
  interfaces:
    - name: "eth0"
      vrf_id: "default"
  
  # Lease defaults
  default_lease_time: 86400  # seconds
  default_renewal_time: 43200
  default_rebind_time: 75600

ha:
  # High Availability clustering
  enabled: true
  cluster_id: "prod-cluster"
  server_id: "dhcp-01"  # Or env var GDHCP_SERVER_ID
  server_name: "dhcp-server-1"
  node_id: 1  # Unique per cluster; lower = preferred primary
  
  # VIP (floating IP)
  vip_address: "10.0.0.10/24"
  vip_interface: "eth0"
  
  # Heartbeat tuning
  heartbeat_interval_ms: 1000
  heartbeat_ttl_ms: 3000
  election_interval_ms: 2000
  
  # Config sync
  config_check_interval_ms: 5000  # Polling fallback
  config_listen_enabled: true    # Use LISTEN/NOTIFY

logging:
  level: "info"  # debug, info, warn, error
  format: "json"  # json or text
  output: "stdout"  # stdout, file, syslog

metrics:
  # Prometheus
  enabled: true
  listen_addr: "0.0.0.0:9090"
  path: "/metrics"

---
```

---

## Chosen Libraries & Versions

### Go Dependencies (server/go.mod)

```go
module github.com/shamalawy/gdhcp

go 1.26

require (
  // Database
  github.com/lib/pq v1.10.9
  // Alternative: github.com/jackc/pgx/v5 v5.5.1
  
  // DHCP packet handling
  github.com/insomniacslk/dhcp v0.10.0
  
  // HA cluster: networking
  github.com/vishvananda/netlink v1.2.0
  github.com/mdlayler/arp v0.0.0-20220512170118-6709a3fc62b7
  
  // CLI
  github.com/spf13/cobra v1.7.0
  github.com/spf13/viper v1.17.0
  
  // HTTP routing
  github.com/gorilla/mux v1.8.1
  
  // Metrics
  github.com/prometheus/client_golang v1.17.0
  
  // Logging
  github.com/sirupsen/logrus v1.9.3
  
  // Utilities
  github.com/google/uuid v1.5.0
  golang.org/x/sync v0.5.0
)
```

### Node Dependencies (web/package.json)

```json
{
  "name": "gdhcp-web",
  "version": "1.0.0",
  "dependencies": {
    "@angular/core": "^17.0.0",
    "@angular/common": "^17.0.0",
    "@angular/router": "^17.0.0",
    "@angular/forms": "^17.0.0",
    "@angular/platform-browser": "^17.0.0",
    "@angular/platform-browser-dynamic": "^17.0.0",
    "rxjs": "^7.8.0",
    "tslib": "^2.6.0",
    "zone.js": "^0.14.0"
  },
  "devDependencies": {
    "@angular-devkit/build-angular": "^17.0.0",
    "@angular/cli": "^17.0.0",
    "@angular/compiler-cli": "^17.0.0",
    "typescript": "^5.2.0"
  }
}
```

---

## HA State Machine

```mermaid
stateDiagram-v2
  [*] --> INITIALIZING: Boot
  
  INITIALIZING --> PRIMARY: Elected (lowest node_id + recent heartbeat)
  INITIALIZING --> SECONDARY: Not elected
  INITIALIZING --> ERROR: DB unreachable
  
  SECONDARY --> PRIMARY: Detected primary failed (heartbeat expired)
  SECONDARY --> ERROR: DB unreachable
  SECONDARY --> SECONDARY: Monitor election (no change)
  
  PRIMARY --> PRIMARY: Heartbeat successful, role unchanged
  PRIMARY --> SECONDARY: Another server elected (lower node_id + fresher heartbeat)
  PRIMARY --> ERROR: Heartbeat write failed (DB partition)
  
  ERROR --> SECONDARY: DB recovered; re-elect
  ERROR --> ERROR: DB still unreachable
  
  PRIMARY --> [*]: Shutdown
  SECONDARY --> [*]: Shutdown
  ERROR --> [*]: Shutdown
```

**State descriptions**:

- **INITIALIZING**: Bootstrap; determining initial role via election
- **PRIMARY**: Holding VIP; answering DHCP; writing heartbeats
- **SECONDARY**: Not holding VIP; answering DHCP; reading heartbeats
- **ERROR**: DB unreachable or HA condition violated; stop serving DHCP; do not hold VIP

---

End of ARCHITECTURE.md
