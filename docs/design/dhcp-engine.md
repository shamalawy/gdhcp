# DHCPv4 Engine Design

## Overview

The DHCPv4 engine is the core packet-handling layer of gdhcp. It processes DHCP client requests on one or more network interfaces, manages DORA/DRAD state machines per client, allocates leases from the backing PostgreSQL store with HA coordination, resolves DHCP options across the inheritance hierarchy (global → template → scope → pool → reservation), and maintains statistics for the dashboard and error visualization.

The engine runs as a standalone component listening on network interfaces, receiving UDP packets on port 67 (broadcast), responding to unicast renewals, and (optionally) sending to a floating virtual IP (VIP) in HA clusters. It coordinates with the API layer to fetch configuration and persist leases atomically.

---

## Architecture Decisions

### 1. Library Choice: github.com/insomniacslk/dhcp

**Decision:** Use `github.com/insomniacslk/dhcp` (specifically the `dhcpv4` package) for DHCP packet encoding/decoding.

**Rationale:**
- **Correctness:** The library has been battle-tested in production (notably in DHCP clients/servers and infrastructure tooling). Hand-rolling packet parsing invites off-by-one errors in option marshalling and TLV handling.
- **Option coverage:** The library supports all IANA option types, with proper type handling for IP addresses, IP lists, strings, integers (8/16/32-bit), booleans, FQDNs, routes, etc. Maintaining this ourselves multiplies maintenance burden.
- **Relay agent support:** Built-in handling for `giaddr` and RFC 3046 Option 82 (Relay Agent Information), including sub-option parsing.
- **Performance:** Optimized marshalling/unmarshalling; negligible overhead vs. hand-rolled code.
- **Maintenance:** Upstream updates handle DHCP RFCs and corner cases. We focus on business logic (leasing, VRF routing, clustering).

**Trade-offs accepted:**
- Dependency on a third-party crate. Mitigation: pin to a stable minor version; crate is widely used, so security updates are timely.
- We cannot arbitrarily drop packets mid-parse (though this is not a requirement).

---

## Package Structure

The engine lives under `internal/dhcp/` with the following layout:

```
internal/
  dhcp/
    engine.go           # Main Engine struct, startup/shutdown
    listener.go         # UDP listener per interface, packet dispatch
    dora.go             # DORA state machine, request handlers
    relay.go            # Relay agent support, giaddr handling
    vrf.go              # VRF selection logic (interface -> VRF mapping)
    allocation.go       # Lease allocation algorithm, Postgres interaction
    options.go          # Option resolution pipeline, encoding
    lease.go            # Lease lifecycle, T1/T2, expiry
    stats.go            # Statistics counters, metrics
    logging.go          # Structured logging for transactions
    errors.go           # DHCP-specific errors
```

---

## Store Layer Interfaces

The engine depends on the store layer via these interfaces (defined in `internal/store/interface.go` or similar):

```go
// LeaseStore handles lease allocation, renewal, and expiry.
type LeaseStore interface {
  // AllocateLease reserves a free IP in the pool for this client (MAC).
  // Returns ErrNoLeases if no IPs available.
  // Atomically handles HA: only one server wins the allocation.
  AllocateLease(ctx context.Context, poolID string, clientMAC string, clientID string) (*store.Lease, error)

  // RenewLease extends expiry for an existing lease.
  // Returns ErrLeaseNotFound if lease expired or not held by this MAC.
  RenewLease(ctx context.Context, leaseID string, clientMAC string, newExpiry time.Time) (*store.Lease, error)

  // ReleaseLease marks a lease as released by client.
  ReleaseLease(ctx context.Context, leaseID string, clientMAC string) error

  // GetLeaseByIP returns the lease for an IP if it exists and is not expired.
  GetLeaseByIP(ctx context.Context, ip net.IP) (*store.Lease, error)

  // GetLeaseByClient returns the active lease for a MAC+Client-ID, if any.
  GetLeaseByClient(ctx context.Context, clientMAC string, clientID string) (*store.Lease, error)

  // ExpireLeasesOlderThan marks all leases with expiry < cutoff as expired (called by reaper).
  ExpireLeasesOlderThan(ctx context.Context, cutoff time.Time) (count int64, error)

  // ReservationForLease checks if a reservation exists for this MAC+scope+pool.
  // Returns nil if no reservation.
  ReservationForLease(ctx context.Context, scopeID, poolID, clientMAC string) (*store.Reservation, error)
}

// ConfigStore fetches DHCP configuration (scopes, pools, options, templates).
type ConfigStore interface {
  // GetVRFByInterface returns the VRF that owns this interface, or a default VRF.
  GetVRFByInterface(ctx context.Context, ifname string) (*store.VRF, error)

  // GetScopesByVRF lists all scopes in a VRF.
  GetScopesByVRF(ctx context.Context, vrfID string) ([]*store.Scope, error)

  // GetScope fetches a scope by ID.
  GetScope(ctx context.Context, scopeID string) (*store.Scope, error)

  // GetPoolsByScope lists all pools in a scope.
  GetPoolsByScope(ctx context.Context, scopeID string) ([]*store.Pool, error)

  // GetPool fetches a pool by ID.
  GetPool(ctx context.Context, poolID string) (*store.Pool, error)

  // GetOptionsByScope fetches options defined on a scope.
  GetOptionsByScope(ctx context.Context, scopeID string) ([]*store.DHCPOption, error)

  // GetOptionsByPool fetches options defined on a pool.
  GetOptionsByPool(ctx context.Context, poolID string) ([]*store.DHCPOption, error)

  // GetGlobalOptions fetches globally-defined DHCP options.
  GetGlobalOptions(ctx context.Context) ([]*store.DHCPOption, error)

  // GetTemplateByID fetches a reusable options template by ID.
  GetTemplateByID(ctx context.Context, templateID string) (*store.Template, error)

  // GetTemplatesByScope lists templates applied to a scope.
  GetTemplatesByScope(ctx context.Context, scopeID string) ([]*store.Template, error)

  // CanAllocateInPool checks if a specific IP can be allocated (not reserved, not in exclusion, etc).
  CanAllocateInPool(ctx context.Context, poolID string, ip net.IP) (bool, error)
}

// StatsRecorder records DHCP statistics for dashboard and monitoring.
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

  // RecordNoLeases increments counter for allocation failures.
  RecordNoLeases(scopeID string)

  // GetStats returns aggregated stats for a scope.
  GetStats(scopeID string) *store.DHCPStats
}

// Logger defines structured logging.
type Logger interface {
  // LogTransaction logs a complete DHCP transaction (one request → one response or NAK).
  LogTransaction(txn *DHCPTransaction)

  // LogError logs an error with context (client MAC, request type, scope, reason).
  LogError(clientMAC string, msgType dhcpv4.MessageType, scopeID string, err error)
}
```

The store types (`store.Lease`, `store.Scope`, `store.Pool`, etc.) are defined in `internal/store/models.go` and initialized from PostgreSQL.

---

## Raw Socket / UDP Listener Strategy

### Per-Interface Listening

Each network interface configured in gdhcp has a dedicated UDP listener on port 67 (DHCP server port) and port 68 (for receiving broadcasts).

**File:** `internal/dhcp/listener.go`

```go
type InterfaceListener struct {
  ifname        string              // e.g., "eth0"
  vrfID         string              // VRF this interface belongs to
  conn          *net.UDPConn        // UDP socket
  bcastAddr     *net.UDPAddr        // Broadcast address for OFFER responses
  vipAddr       *net.UDPAddr        // VIP address (if in HA cluster), else nil
  ctx           context.Context
  cancel        context.CancelFunc
  pktChan       chan *DHCPPacket    // Received packet queue (buffered, size 1000)
}

type DHCPPacket struct {
  ReceivedAt    time.Time
  Interface     string
  SourceAddr    *net.UDPAddr
  MessageType   dhcpv4.MessageType
  Packet        *dhcpv4.DHCPv4
  XSID          [4]byte             // Transaction ID from packet header
  RelayedFromVRF string              // Non-empty if this came via relay agent (option 82)
}
```

**Packet flow:**
1. Listener reads UDP packets on interface.
2. On DISCOVER/REQUEST: respond via broadcast if no relay agent; else send unicast to `giaddr`.
3. On INFORM/RENEW with `ciaddr`: respond via unicast to requesting IP (or relay).
4. On DECLINE/RELEASE: no response (per RFC).

**Socket binding:**
- Bind to `0.0.0.0:67` (all interfaces) OR bind per interface (SO_BINDTODEVICE) if kernel supports it. Start with all-interfaces approach for simplicity; permit operator to override per-listener settings.
- Broadcast TX: set SO_BROADCAST on socket.
- Unicast TX on HA VIP: if cluster is configured with VIP, response packets use VIP as source when responding to relayed requests.

---

## Relay Agent Support (Option 82)

**File:** `internal/dhcp/relay.go`

Relay agents add option 82 (Relay Agent Information) to DISCOVER/REQUEST messages. The engine must:

1. **Parse option 82 sub-options:**
   - Circuit ID (sub-option 1): identifies the relay's interface.
   - Remote ID (sub-option 2): client identifier from relay's perspective.
   - Server ID (sub-option 11): server ID inserted by relay (if present).

2. **Extract giaddr:** The relay's IP address (relay gateway address).

3. **VRF mapping via giaddr:** The `giaddr` field in the DHCP header identifies which VRF/subnet the client is on. Match `giaddr` against known subnets in the VRF's scopes to find the serving scope.

4. **Response routing:** Return responses to `giaddr`, not directly to client (per RFC 2131 §4.2.1).

```go
func (e *Engine) handleRelayedDiscover(ctx context.Context, pkt *DHCPPacket) error {
  // Extract giaddr from DHCP header
  relayAddr := net.IP(pkt.Packet.GatewayIPAddress)
  
  // Find VRF containing a scope with this giaddr
  scope, err := e.store.FindScopeByGiaddr(ctx, relayAddr)
  if err != nil {
    return fmt.Errorf("relay from unknown giaddr %s: %w", relayAddr, err)
  }
  
  // Continue normal DISCOVER processing with identified scope
  return e.handleDiscover(ctx, pkt, scope)
}
```

---

## VRF Selection Logic

**File:** `internal/dhcp/vrf.go`

VRF selection determines which DHCP scope(s) to check for the given request.

**Algorithm:**
1. If request is relayed (has `giaddr`):
   - Match `giaddr` against known subnets in store to find serving VRF and scope.
2. If request is direct (no relay, `ciaddr` = 0):
   - Look up receiving interface → VRF mapping from config store.
   - Iterate scopes in that VRF; find matching scope subnet.
3. If request is renewal (RENEWING/REBINDING state, `ciaddr` populated):
   - Find existing lease for `ciaddr`.
   - Use the pool's VRF/scope.

**Default VRF:** If no VRF is configured for an interface, use a default VRF labeled "default".

```go
type VRFSelector struct {
  store ConfigStore
}

func (sel *VRFSelector) SelectVRF(ctx context.Context, pkt *DHCPPacket) (*store.VRF, *store.Scope, error) {
  if pkt.Packet.GatewayIPAddress != nil {
    // Relayed request
    return sel.selectVRFByGiaddr(ctx, pkt.Packet.GatewayIPAddress)
  }

  if !isZeroIP(pkt.Packet.ClientIPAddress) {
    // Renewal with ciaddr
    return sel.selectVRFByIP(ctx, pkt.Packet.ClientIPAddress)
  }

  // Direct request
  return sel.selectVRFByInterface(ctx, pkt.Interface)
}
```

---

## DORA State Machine

**File:** `internal/dhcp/dora.go`

The engine processes four request types per the DHCP state machine:

### DISCOVER (Message Type 1)

1. Receive DISCOVER from client.
2. Find eligible scope (via VRF selection).
3. Allocate free IP from a pool (or error if none available).
4. Compose OFFER with allocated IP, server ID, lease time, merged options.
5. Respond with OFFER via broadcast (or relay).
6. Record stats: `RecordDiscover()`, `RecordOffer()`.

**State:** Client transitions to SELECTING after OFFER.

### REQUEST (Message Type 3)

Handles three cases:

**a) SELECTING state (after OFFER):**
- Client has received one or more OFFERs and selected one.
- DISCOVER-caused-IP field in options (RFC 2131 §4.3.2).
- Validate offered IP is still available.
- Send ACK with options, or NAK if IP no longer available.

**b) INIT-REBOOT state (restart without binding):**
- Client supplies `requested-ip` option; no `ciaddr`.
- Validate requested IP is in a known scope; if not, send NAK.
- Send ACK or NAK accordingly.

**c) RENEWING/REBINDING (lease extension):**
- Client has `ciaddr` set; typically also supplies `server-id` (RENEWING).
- Renew the lease; extend expiry.
- Send ACK or NAK.

**Handling:** Check `request-ip` option (code 50) and `server-identifier` option (code 54).

### DECLINE (Message Type 4)

1. Client reports the allocated IP is unreachable (e.g., ARP conflict).
2. Mark the lease as unusable (state = DECLINED).
3. Allocate a new IP if DISCOVER follows.
4. No response sent (per RFC).
5. Record stats: `RecordDecline()`.

### RELEASE (Message Type 7)

1. Client explicitly releases the lease.
2. Mark lease as RELEASED.
3. IP returns to free pool.
4. No response sent (per RFC).
5. Record stats: `RecordRelease()`.

### INFORM (Message Type 8)

1. Client already has an IP (e.g., from static config).
2. Requests additional options only (no IP allocation).
3. Respond with ACK containing options (no offered IP).
4. Lease is not recorded (INFORM does not establish a binding).

### State Machine Diagram

```
                    Client
                      |
                    [Boot]
                      |
      +-------+-------+-------+
      |       |       |       |
   DISCOVER INIT-REBOOT RENEW  REBIND
      |       |       |       |
   (OFFER)    |       |       |
      |       |       |       |
   OFFER -----+-------+-------+
      |               |
   SELECT          (RENEW)
      |               |
   REQUEST ----------+
      |
  (ACK or NAK)
      |
   BOUND ---> REBINDING ---> REBIND ---> ACK/NAK
                   ^                       |
                   |_______________________|
                                (T2 timeout)
```

---

## Lease Allocation Algorithm (HA-Safe)

**File:** `internal/dhcp/allocation.go`

Multiple cluster nodes may simultaneously attempt to allocate IPs from the same pool. The algorithm must be atomic.

### Strategy: Postgres Row-Level Locking + Conditional Update

Each IP address in a pool is stored as a row in the database:

**Table: `ip_allocations`**
```sql
CREATE TABLE ip_allocations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pool_id UUID NOT NULL REFERENCES pools(id) ON DELETE CASCADE,
  ip_address INET NOT NULL,
  mac_address MACADDR,
  client_id TEXT,
  state VARCHAR(20) DEFAULT 'free',  -- free, allocated, reserved, declined
  lease_start TIMESTAMP,
  lease_expiry TIMESTAMP,
  server_id VARCHAR(50),             -- cluster server that owns this lease
  created_at TIMESTAMP DEFAULT now(),
  updated_at TIMESTAMP DEFAULT now(),
  
  CONSTRAINT unique_ip_per_pool UNIQUE(pool_id, ip_address),
  CONSTRAINT check_state CHECK(state IN ('free', 'allocated', 'reserved', 'declined'))
);

CREATE INDEX idx_allocations_pool_state ON ip_allocations(pool_id, state);
CREATE INDEX idx_allocations_expiry ON ip_allocations(lease_expiry);
```

### Allocation Flow

```go
func (e *Engine) allocateLease(ctx context.Context, pool *store.Pool, clientMAC string, clientID string) (*store.Lease, error) {
  txn, err := e.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
  if err != nil {
    return nil, err
  }
  defer txn.Rollback()

  // 1. Check for existing lease for this client
  existingLease, err := txn.GetLeaseByClient(ctx, clientMAC, clientID)
  if err == nil && existingLease.Expiry.After(time.Now()) {
    return existingLease, nil // Return existing lease if not expired
  }

  // 2. Lock and find first free IP in pool
  ip, err := txn.SelectFreeIPInPool(ctx, pool.ID)
  if err != nil {
    return nil, fmt.Errorf("no free IPs in pool: %w", err)
  }

  // 3. Atomically transition IP to allocated
  leaseExpiry := time.Now().Add(pool.LeaseTime)
  result, err := txn.Exec(`
    UPDATE ip_allocations 
    SET state = 'allocated', 
        mac_address = $1,
        client_id = $2,
        lease_start = now(),
        lease_expiry = $3,
        server_id = $4
    WHERE pool_id = $5 AND ip_address = $6 AND state = 'free'
  `, clientMAC, clientID, leaseExpiry, e.serverID, pool.ID, ip.String())
  
  if result.RowsAffected == 0 {
    return nil, errors.New("IP already allocated by concurrent request")
  }

  if err = txn.Commit(); err != nil {
    return nil, err
  }

  return &store.Lease{
    IP:        ip,
    MAC:       clientMAC,
    ClientID:  clientID,
    ExpireAt:  leaseExpiry,
    ServerID:  e.serverID,
  }, nil
}
```

**Why this works:**
- Serializable isolation ensures no phantom reads.
- `SELECT … FOR UPDATE` locks the row; only one server wins the UPDATE.
- If row is already allocated, UPDATE returns 0 rows; server retries next IP.
- Expiry reaper periodically marks expired leases as free.

### Reservations

Reservations are stored in a separate table and pre-allocated:

**Table: `reservations`**
```sql
CREATE TABLE reservations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope_id UUID NOT NULL REFERENCES scopes(id),
  pool_id UUID NOT NULL REFERENCES pools(id),
  mac_address MACADDR NOT NULL,
  ip_address INET NOT NULL,
  description TEXT,
  created_at TIMESTAMP DEFAULT now(),

  CONSTRAINT unique_reservation UNIQUE(mac_address, pool_id)
);
```

On allocation, check if `mac_address` has a reservation in the pool; if yes, mark that IP as allocated (not any free IP).

---

## Option Resolution Pipeline

**File:** `internal/dhcp/options.go`

Options are inherited from global → template → scope → pool → reservation, with later levels overriding earlier ones.

### Resolution Order

1. **Fetch all sources (in priority order):**
   - Global options (stored in `dhcp_options` with `context_type = 'global'`)
   - Template options for this scope (if templates applied)
   - Scope options (stored with `context_id = scope_id`)
   - Pool options (stored with `context_id = pool_id`)
   - Reservation-specific options (if reservation exists)

2. **Merge options by code:**
   - Build a map: `code → Option`.
   - Later sources override earlier; last one wins.

3. **Filter by Client Parameter Request List (PRL):**
   - Client sends Option 55 (Parameter Request List) containing codes it wants.
   - If PRL is present, include ONLY those options in the response.
   - Always include certain mandatory options (subnet mask, router, lease time, server ID).

4. **Encode each option by type:**
   - **IP address** (codes 1, 3, 28, etc.): `net.IP` → 4 bytes
   - **IP list** (codes 8, 42, etc.): slice of `net.IP` → concatenated 4-byte chunks
   - **String** (codes 12, 15, 54, etc.): `string` → bytes
   - **Uint8** (codes 19, 23, 34, etc.): `uint8` → 1 byte
   - **Uint16** (codes 13, 26, etc.): `uint16` → 2 bytes, big-endian
   - **Uint32** (codes 2, 51, etc.): `uint32` → 4 bytes, big-endian
   - **Boolean** (codes 27, 39, etc.): `bool` → 1 byte (0 or 1)
   - **Hex** (codes 61, 97, etc.): raw hex blob → bytes
   - **FQDN** (codes 81, 104, etc.): domain name → wire format (DNS label compression)
   - **Route list** (codes 33, 121, etc.): CIDR + router → encoded as per RFC 3442

```go
type OptionResolver struct {
  store ConfigStore
}

func (res *OptionResolver) ResolveOptions(ctx context.Context, req *DHCPRequest) (*OptionSet, error) {
  result := NewOptionSet()

  // 1. Global options
  globalOpts, err := res.store.GetGlobalOptions(ctx)
  if err != nil {
    return nil, err
  }
  for _, opt := range globalOpts {
    result.Set(opt.Code, opt)
  }

  // 2. Template options (if applicable)
  templates, err := res.store.GetTemplatesByScope(ctx, req.Scope.ID)
  if err != nil {
    return nil, err
  }
  for _, tmpl := range templates {
    for _, opt := range tmpl.Options {
      result.Set(opt.Code, opt)
    }
  }

  // 3. Scope options
  scopeOpts, err := res.store.GetOptionsByScope(ctx, req.Scope.ID)
  if err != nil {
    return nil, err
  }
  for _, opt := range scopeOpts {
    result.Set(opt.Code, opt)
  }

  // 4. Pool options
  poolOpts, err := res.store.GetOptionsByPool(ctx, req.Pool.ID)
  if err != nil {
    return nil, err
  }
  for _, opt := range poolOpts {
    result.Set(opt.Code, opt)
  }

  // 5. Reservation options (if reservation exists)
  if req.Reservation != nil {
    for _, opt := range req.Reservation.Options {
      result.Set(opt.Code, opt)
    }
  }

  // 6. Filter by Parameter Request List
  if len(req.PRL) > 0 {
    result.FilterByPRL(req.PRL)
  }

  return result, nil
}

func (set *OptionSet) Encode() ([]byte, error) {
  var buf bytes.Buffer
  for code, opt := range set.options {
    var value []byte
    
    switch opt.Type {
    case "ip":
      value = net.ParseIP(opt.Value).To4()
    case "ip_list":
      for _, ipStr := range strings.Split(opt.Value, ",") {
        value = append(value, net.ParseIP(strings.TrimSpace(ipStr)).To4()...)
      }
    case "string":
      value = []byte(opt.Value)
    case "uint8":
      u8, _ := strconv.ParseUint(opt.Value, 10, 8)
      value = []byte{byte(u8)}
    case "uint16":
      u16, _ := strconv.ParseUint(opt.Value, 10, 16)
      value = append(value, byte(u16>>8), byte(u16))
    case "uint32":
      u32, _ := strconv.ParseUint(opt.Value, 10, 32)
      value = append(value, byte(u32>>24), byte(u32>>16), byte(u32>>8), byte(u32))
    case "bool":
      if opt.Value == "true" {
        value = []byte{1}
      } else {
        value = []byte{0}
      }
    case "route_list":
      // RFC 3442: Classless Static Route Option
      value = encodeRouteList(opt.Value)
    default:
      return nil, fmt.Errorf("unknown option type: %s", opt.Type)
    }

    // Append TLV: Code (1 byte) + Length (1 byte) + Value
    buf.WriteByte(byte(code))
    buf.WriteByte(byte(len(value)))
    buf.Write(value)
  }
  return buf.Bytes(), nil
}
```

---

## Lease Lifecycle & Expiry Reaper

**File:** `internal/dhcp/lease.go`

### Lease States

```go
type LeaseState string

const (
  LeaseAllocated LeaseState = "allocated"
  LeaseReleased  LeaseState = "released"
  LeaseExpired   LeaseState = "expired"
  LeaseDeclined  LeaseState = "declined"
)

type Lease struct {
  ID         string
  IP         net.IP
  MAC        string
  ClientID   string
  State      LeaseState
  AllocatedAt time.Time
  ExpireAt   time.Time
  T1         time.Time // Renewal time (50% of lease)
  T2         time.Time // Rebinding time (87.5% of lease)
  PoolID     string
  ServerID   string    // Which cluster node owns this lease
}
```

### T1/T2 Timing

When offering a lease with duration `D`:
- **T1** = now + 0.5×D (renewal time; client sends RENEW)
- **T2** = now + 0.875×D (rebinding time; client sends REBIND to all servers)
- **Expiry** = now + D (lease expires; IP returns to free pool)

Encode T1 and T2 in the DHCP response:
- Option 58 (Renewal Time Value): T1 in seconds
- Option 59 (Rebinding Time Value): T2 in seconds

### Expiry Reaper

Run once per minute (or configurable interval):

```go
func (e *Engine) runExpiryReaper(ctx context.Context) {
  ticker := time.NewTicker(1 * time.Minute)
  defer ticker.Stop()

  for {
    select {
    case <-ctx.Done():
      return
    case <-ticker.C:
      cutoff := time.Now()
      count, err := e.store.ExpireLeasesOlderThan(ctx, cutoff)
      if err != nil {
        e.logger.LogError("", 0, "", fmt.Errorf("expiry reaper failed: %w", err))
      } else if count > 0 {
        e.logger.LogInfo("expired %d leases", count)
      }
    }
  }
}
```

---

## Optional Ping-Check Before OFFER

**File:** `internal/dhcp/engine.go` (configuration option)

Before sending an OFFER, the engine can send an ICMP Echo Request (ping) to the allocated IP to verify it is not in use (conflict detection).

**Configuration:**
```go
type EngineConfig struct {
  PingCheckEnabled  bool
  PingCheckTimeout  time.Duration // e.g., 100ms
}
```

**Flow:**
```go
func (e *Engine) shouldAllocateIP(ctx context.Context, ip net.IP) (bool, error) {
  if !e.cfg.PingCheckEnabled {
    return true, nil
  }

  pinger, err := net.DialIP("ip4:icmp", nil, &net.IPAddr{IP: ip})
  if err != nil {
    return false, err
  }
  defer pinger.Close()

  msg := icmp.Message{
    Type: ipv4.Echo, Code: 0,
    Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: 1, Data: []byte("gdhcp")},
  }
  msgBytes, _ := msg.Marshal(nil)

  if err := pinger.SetDeadline(time.Now().Add(e.cfg.PingCheckTimeout)); err != nil {
    return false, err
  }

  if _, err := pinger.Write(msgBytes); err != nil {
    return true, nil // No response = IP is free
  }

  if _, err := pinger.Read(make([]byte, 1500)); err != nil {
    return true, nil // Read timeout = IP is free
  }

  return false, nil // Received response = IP is in use
}
```

**Note:** Ping check is optional and disabled by default (performance impact). Use for environments where IP conflicts are frequent.

---

## Statistics Counters

**File:** `internal/dhcp/stats.go`

Counters track message type distribution per scope for dashboard visualization and alerting.

**Storage:**
```sql
CREATE TABLE dhcp_stats (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope_id UUID NOT NULL REFERENCES scopes(id) ON DELETE CASCADE,
  discovers BIGINT DEFAULT 0,
  offers BIGINT DEFAULT 0,
  requests BIGINT DEFAULT 0,
  acks BIGINT DEFAULT 0,
  naks BIGINT DEFAULT 0,
  declines BIGINT DEFAULT 0,
  releases BIGINT DEFAULT 0,
  no_leases BIGINT DEFAULT 0,        -- Allocation failures
  errors BIGINT DEFAULT 0,           -- Parse errors, etc.
  hour_bucket TIMESTAMP DEFAULT date_trunc('hour', now()),
  updated_at TIMESTAMP DEFAULT now(),

  CONSTRAINT unique_scope_hour UNIQUE(scope_id, hour_bucket)
);
```

**In-memory aggregation:**
```go
type StatsCollector struct {
  mu      sync.Mutex
  buckets map[string]*ScopeBucket  // scopeID → bucket

  flushTicker *time.Ticker
  flushChan   chan struct{}
}

type ScopeBucket struct {
  Discovers int64
  Offers    int64
  Requests  int64
  Acks      int64
  Naks      int64
  Declines  int64
  Releases  int64
  NoLeases  int64
  Errors    int64
}

func (sc *StatsCollector) RecordOffer(scopeID string) {
  sc.mu.Lock()
  defer sc.mu.Unlock()
  sc.buckets[scopeID].Offers++
}

func (sc *StatsCollector) Flush(ctx context.Context) error {
  sc.mu.Lock()
  buckets := sc.buckets
  sc.buckets = make(map[string]*ScopeBucket)
  sc.mu.Unlock()

  for scopeID, bucket := range buckets {
    err := sc.db.UpsertStats(ctx, scopeID, bucket)
    if err != nil {
      return err
    }
  }
  return nil
}
```

Run a flush goroutine every 5 minutes.

---

## Structured Logging

**File:** `internal/dhcp/logging.go`

Every DHCP transaction (request → response/NAK) is logged with structured fields for error diagnostics and audit trails.

**Log fields:**
- `timestamp`: ISO 8601 timestamp
- `transaction_id`: DHCP XID (transaction ID)
- `client_mac`: Client MAC address (format: `aa:bb:cc:dd:ee:ff`)
- `client_id`: Client identifier (from Option 61, if present)
- `msg_type`: DHCP message type name (DISCOVER, OFFER, REQUEST, ACK, NAK, DECLINE, RELEASE)
- `scope_id`: Scope the request matched
- `pool_id`: Pool (if applicable)
- `allocated_ip`: IP allocated/offered/ACK'd (if applicable)
- `lease_duration`: Lease time in seconds (if applicable)
- `relay_agent`: `giaddr` if relayed request
- `server_id`: Cluster server ID that handled the request
- `result`: "OK", "NAK", "ERROR"
- `reason`: Short reason string if NAK or ERROR
- `duration_ms`: Time from request received to response sent

**Example log entry:**
```json
{
  "timestamp": "2026-08-29T10:23:45.123Z",
  "transaction_id": "a1b2c3d4",
  "client_mac": "aa:bb:cc:dd:ee:ff",
  "msg_type": "DISCOVER",
  "scope_id": "scope-prod-subnet",
  "result": "OK",
  "allocated_ip": "10.0.1.50",
  "lease_duration": 86400,
  "server_id": "gdhcp-primary",
  "duration_ms": 3
}
```

**Logging backend:**
- Write to stdout (JSON format) or structured logging library (e.g., `github.com/rs/zerolog` or stdlib `log/slog`).
- Operator configures log level and output destination.
- High-frequency logging (one per DHCP transaction) requires buffering; use async I/O or a log channel.

---

## Engine Startup & Shutdown

**File:** `internal/dhcp/engine.go`

```go
type Engine struct {
  cfg              *EngineConfig
  store            LeaseStore
  configStore      ConfigStore
  statsRecorder    StatsRecorder
  logger           Logger

  listeners        map[string]*InterfaceListener  // ifname → listener
  dora             *DORAHandler
  optResolver      *OptionResolver
  vrfSelector      *VRFSelector

  ctx              context.Context
  cancel           context.CancelFunc
  wg               sync.WaitGroup

  // Cluster
  serverID         string                        // Unique ID for this server
  vipAddr          string                        // Virtual IP (if in HA cluster)
}

func NewEngine(cfg *EngineConfig) *Engine {
  ctx, cancel := context.WithCancel(context.Background())
  return &Engine{
    cfg:       cfg,
    listeners: make(map[string]*InterfaceListener),
    ctx:       ctx,
    cancel:    cancel,
    serverID:  cfg.ServerID,
    vipAddr:   cfg.VirtualIP,
  }
}

func (e *Engine) Start() error {
  // 1. Load configuration (interfaces, VRFs, scopes, pools)
  interfaces, err := e.configStore.GetInterfaces(e.ctx)
  if err != nil {
    return err
  }

  // 2. Create listeners for each interface
  for _, iface := range interfaces {
    listener := &InterfaceListener{
      ifname:    iface.Name,
      vrfID:     iface.VRFID,
      pktChan:   make(chan *DHCPPacket, 1000),
    }

    if err := listener.Open(); err != nil {
      return fmt.Errorf("failed to open listener on %s: %w", iface.Name, err)
    }

    e.listeners[iface.Name] = listener

    // 3. Start goroutine to process packets from this listener
    e.wg.Add(1)
    go e.processListenerPackets(listener)
  }

  // 4. Start expiry reaper
  e.wg.Add(1)
  go e.runExpiryReaper(e.ctx)

  // 5. Start stats flusher
  e.wg.Add(1)
  go e.statsRecorder.Flush(e.ctx)

  return nil
}

func (e *Engine) Stop() error {
  e.cancel()
  
  // Close all listeners
  for _, listener := range e.listeners {
    listener.Close()
  }

  // Wait for all goroutines to exit
  e.wg.Wait()
  return nil
}

func (e *Engine) processListenerPackets(listener *InterfaceListener) {
  defer e.wg.Done()

  for {
    select {
    case <-e.ctx.Done():
      return
    case pkt := <-listener.pktChan:
      e.handlePacket(pkt)
    }
  }
}

func (e *Engine) handlePacket(pkt *DHCPPacket) {
  switch pkt.Packet.MessageType {
  case dhcpv4.MessageTypeDiscover:
    if err := e.dora.HandleDiscover(e.ctx, pkt); err != nil {
      e.logger.LogError(pkt.Packet.ClientHWAddress.String(), pkt.MessageType, "", err)
    }
  case dhcpv4.MessageTypeRequest:
    if err := e.dora.HandleRequest(e.ctx, pkt); err != nil {
      e.logger.LogError(pkt.Packet.ClientHWAddress.String(), pkt.MessageType, "", err)
    }
  case dhcpv4.MessageTypeDecline:
    if err := e.dora.HandleDecline(e.ctx, pkt); err != nil {
      e.logger.LogError(pkt.Packet.ClientHWAddress.String(), pkt.MessageType, "", err)
    }
  case dhcpv4.MessageTypeRelease:
    if err := e.dora.HandleRelease(e.ctx, pkt); err != nil {
      e.logger.LogError(pkt.Packet.ClientHWAddress.String(), pkt.MessageType, "", err)
    }
  case dhcpv4.MessageTypeInform:
    if err := e.dora.HandleInform(e.ctx, pkt); err != nil {
      e.logger.LogError(pkt.Packet.ClientHWAddress.String(), pkt.MessageType, "", err)
    }
  }
}
```

---

## Error Handling

**File:** `internal/dhcp/errors.go`

DHCP-specific errors must be distinguishable for logging and error visualization on the dashboard.

```go
package dhcp

import "errors"

var (
  ErrNoLeasesAvailable  = errors.New("no free IPs in pool")
  ErrIPNotInScope       = errors.New("requested IP not in any pool")
  ErrScopeNotFound      = errors.New("scope not found for request")
  ErrInvalidRequest     = errors.New("invalid DHCP request format")
  ErrLeaseNotFound      = errors.New("lease not found")
  ErrIPConflict         = errors.New("IP address in use")
  ErrRelayNotSupported  = errors.New("relay agent not configured")
)

type RequestError struct {
  Err    error
  Code   int  // DHCP message type that caused error
  Reason string
}
```

---

## Package Initialization & Dependencies

**File:** `internal/dhcp/init.go` or initialization in main server setup:

```go
func InitDHCPEngine(
  cfg *config.DHCPEngineConfig,
  leaseStore store.LeaseStore,
  configStore store.ConfigStore,
  statsRec store.StatsRecorder,
  logger store.Logger,
) (*Engine, error) {

  engine := &Engine{
    cfg:           cfg,
    store:         leaseStore,
    configStore:   configStore,
    statsRecorder: statsRec,
    logger:        logger,
    listeners:     make(map[string]*InterfaceListener),
  }

  engine.vrfSelector = &VRFSelector{store: configStore}
  engine.optResolver = &OptionResolver{store: configStore}
  engine.dora = &DORAHandler{
    engine:  engine,
    store:   leaseStore,
    config:  configStore,
    logger:  logger,
  }

  return engine, nil
}
```

---

## Testing Strategy

Unit tests for each subpackage:
- `dhcp_listener_test.go`: Socket binding, packet reading, relay detection.
- `dhcp_dora_test.go`: DISCOVER → OFFER, REQUEST → ACK, state machine transitions.
- `dhcp_allocation_test.go`: Concurrent allocation under HA, expiry logic.
- `dhcp_options_test.go`: Option merging, filtering, encoding of all types.
- `dhcp_vrf_test.go`: VRF selection by interface, giaddr, ciaddr.

Integration tests:
- `integration_test.go`: End-to-end DISCOVER → ACK → RENEW → RELEASE with real database.
- HA concurrent allocation: spawn 2+ engine instances, verify no IP conflicts.

---

## Configuration File Example

The engine reads configuration from `cmd/gdhcpd/config.yaml` (or environment):

```yaml
dhcp:
  enabled: true
  server_id: "gdhcp-primary-1"
  
  listeners:
    - interface: "eth0"
      vrf_id: "default"
    - interface: "eth1"
      vrf_id: "prod"
  
  virtual_ip: "10.0.0.254"  # HA VIP (optional)
  
  options:
    ping_check_enabled: false
    ping_check_timeout: "100ms"
    
  lease_times:
    default: "24h"
    min: "1h"
    max: "7d"
  
  expiry_reaper_interval: "1m"
  stats_flush_interval: "5m"
  
  logging:
    level: "info"
    format: "json"
    output: "stdout"
```

---

## Summary

The DHCPv4 engine uses `github.com/insomniacslk/dhcp` for packet handling, manages per-interface UDP listeners grouped by VRF, implements a complete DORA state machine with relay agent and HA support, allocates leases atomically via PostgreSQL serializable transactions, resolves options through a four-level inheritance pipeline, tracks statistics per scope, and maintains structured transaction logs for diagnostics and dashboard error visualization. The modular package structure isolates concerns (listening, DORA logic, allocation, options, lease lifecycle) and defines clear interfaces to the backing store layer.
