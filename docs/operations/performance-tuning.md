# Performance Tuning

Guidance for sizing gdhcp deployments. Numbers here are starting points for
capacity planning, not guarantees — validate against your own lease churn
and pool sizes.

## Database connection pool

`database.max_open_conns` / `max_idle_conns` (config.yaml) — default 25/5.
Each server node holds its own pool against the shared PostgreSQL instance,
so total connections scale with node count: an N-node cluster at the
default settings can open up to `N * 25` connections. Size Postgres's
`max_connections` accordingly (leave headroom for `psql`/admin tooling and
any read replicas), or lower `max_open_conns` per node in large clusters.

`database.conn_max_lifetime` (default `5m`) recycles connections
periodically — useful behind connection poolers (PgBouncer) or managed
Postgres services that terminate long-lived connections server-side.

## Lease allocation contention

Amendment A6's allocation algorithm retries up to 3 rounds on a lost
`INSERT ... ON CONFLICT` race before giving up (recorded as `no_leases`).
Watch `gdhcp_dhcp_allocation_retries_bucket` and
`gdhcp_lease_allocation_conflicts_total` (docs/operations/monitoring.md):
sustained high retry counts on a pool mean either
- the pool is genuinely near-full (check utilization via
  `GET /api/v1/dashboard/stats`) — grow the pool/prefix, or
- multiple HA nodes are racing for the same pool under heavy simultaneous
  DISCOVER traffic — normal at moderate scale (READ COMMITTED + the unique
  constraint on `(pool_id, ip_address)` is the actual serialization point,
  not the retry loop itself), but if `no_leases` shows up in transaction
  logs (`docs/design/dhcp-engine.md`'s structured logging), the pool is the
  bottleneck, not contention.

## Option & config caching

`database.option_cache_ttl` (default `60s`) controls how long resolved DHCP
option sets are cached in memory per scope/pool before re-querying
Postgres. Lower it for environments where option values change frequently
and must propagate fast; raise it (and rely on the LISTEN/NOTIFY
invalidation path — `docs/CONTRACTS.md` Part 7) to cut DB load in
option-heavy, high-DISCOVER-rate deployments. Cache invalidation on write is
immediate regardless of TTL — the TTL only bounds staleness from a *missed*
notification (e.g. a follower that was briefly disconnected), so raising it
is safe.

## Heartbeat / election intervals

`ha.heartbeat_interval_ms` / `heartbeat_ttl_ms` / `election_interval_ms`
trade failover speed against false-positive risk:
- Tighter intervals (lower ms) → faster failover detection, but more
  sensitive to transient DB latency spikes causing an unnecessary election
  (watch `gdhcp_heartbeat_write_duration_ms_bucket` p95/p99 — it must stay
  comfortably under `heartbeat_interval_ms`, ideally under half of it)
- Looser intervals → fewer false failovers, longer client-visible outage
  (worst case, roughly `heartbeat_ttl_ms + election_interval_ms`) when a
  real failure happens

Defaults (1000ms heartbeat / 3000ms TTL / 2000ms election) assume
low-latency, same-datacenter Postgres. Cross-region or otherwise
higher-latency DB links should widen `heartbeat_ttl_ms` proportionally to
observed p99 write latency, not just leave it at the default.

## Lease retention

`database.lease_retention_days` (default 90) bounds how long expired/released
lease history is kept before cleanup — mostly a table-bloat/storage
concern on high-churn networks (short lease times, many transient clients:
guest Wi-Fi, IoT). Lower it if `dhcp_leases` growth is a storage or
`VACUUM` concern; raise it if lease history is used for auditing/troubleshooting
beyond 90 days.

## DHCP listener / interface count

Each entry in `dhcp.interfaces` gets its own UDP listener goroutine
(`server/internal/dhcp/listener.go`). This scales fine to the handful of
interfaces a typical multi-VRF deployment needs; there's no meaningful
per-interface overhead to plan around beyond making sure each interface's
VRF-scoped pools are appropriately sized for that segment's client count.

## General sizing starting points

| Scale | Nodes | DB `max_open_conns` | Notes |
|---|---|---|---|
| Small (single site, <1k leases) | 1 (or 2 for HA) | 10–25 (default) | Defaults are fine as-is |
| Medium (multi-VRF, several k leases) | 2–3 | 25 (default), watch pool contention | Consider PgBouncer if node count grows |
| Large (many sites/VRFs, tens of k+ leases) | per-site pairs, shared or federated DB | tuned per node, pooler strongly recommended | Widen heartbeat_ttl_ms if DB is cross-region; monitor `gdhcp_db_*` connection-pool metrics closely |

Always validate against `GET /api/v1/dashboard/stats` and the Prometheus
metrics in `docs/operations/monitoring.md` rather than these table entries
alone — actual client behavior (lease time, renewal patterns, retry storms)
varies more than node/pool count does.
