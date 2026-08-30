# Monitoring

gdhcp exposes Prometheus metrics on `metrics.listen_addr` (default
`0.0.0.0:9090`) at `metrics.path` (default `/metrics`) — separate from the
API's `http`/8080 port, so metrics scraping doesn't share a port (or auth)
with the REST API. Registered in `server/internal/metrics/registry.go` per
`docs/CONTRACTS.md` Part 8.

## Scraping

**Bare metal / Docker**: point Prometheus at `<host>:9090/metrics` directly.

**Kubernetes**: the DaemonSet pods already carry
`prometheus.io/scrape: "true"`, `prometheus.io/port: "9090"`,
`prometheus.io/path: "/metrics"` annotations (`deploy/kubernetes/base/gdhcp/daemonset.yaml`)
for annotation-based Prometheus discovery; the `gdhcp-api` Service also
exposes port 9090 if you'd rather scrape through it.

## Key metrics

Naming convention: `gdhcp_{subsystem}_{metric_name}_{unit}`, labeled by
entity ID (scope/pool/cluster/server) where relevant.

| Metric | Type | What it tells you |
|---|---|---|
| `gdhcp_dhcp_discover_total{scope_id}` | counter | DISCOVER volume per scope |
| `gdhcp_dhcp_offer_total{scope_id}` | counter | OFFERs sent — compare to discover_total for drop rate |
| `gdhcp_dhcp_allocation_retries_bucket{scope_id,le}` | histogram | Allocation contention (amendment A6's retry loop, capped at 3) |
| `gdhcp_lease_allocation_conflicts_total{pool_id}` | counter | `INSERT ... ON CONFLICT` races lost — high values mean an undersized pool or hot contention |
| `gdhcp_heartbeat_write_duration_ms_bucket{server_id,le}` | histogram | HA heartbeat write latency — DB slowness shows here first |
| `gdhcp_election_query_duration_ms_bucket{cluster_id,le}` | histogram | Election query latency |
| `gdhcp_vip_assignment_failures_total{cluster_id}` | counter | Should stay 0 — nonzero means a node tried and failed to take the VIP (permissions or netlink error; see troubleshooting.md) |
| `gdhcp_db_partition_detected_total{cluster_id}` | counter | Should stay 0 — nonzero is the split-brain signal (troubleshooting.md) |

Standard Go/process metrics (`process_*`, `go_*`) and
`gdhcp_db_*`/`gdhcp_api_*` (connection pool saturation, request
latency/count by route+status) are also registered — useful for basic
capacity dashboards even without gdhcp-specific alerting.

## Example alerts (Prometheus rule syntax)

```yaml
groups:
  - name: gdhcp
    rules:
      - alert: GDHCPDown
        expr: up{job="gdhcp"} == 0
        for: 2m
        labels: {severity: critical}
        annotations:
          summary: "gdhcp instance {{ $labels.instance }} is unreachable"

      - alert: GDHCPHighAllocationConflicts
        expr: rate(gdhcp_lease_allocation_conflicts_total[5m]) > 5
        for: 10m
        labels: {severity: warning}
        annotations:
          summary: "Pool {{ $labels.pool_id }} has heavy lease-allocation contention"
          description: "Consider whether the pool is undersized for its request rate."

      - alert: GDHCPVIPAssignmentFailure
        expr: increase(gdhcp_vip_assignment_failures_total[15m]) > 0
        labels: {severity: critical}
        annotations:
          summary: "Cluster {{ $labels.cluster_id }} failed to assign its VIP"
          description: "See docs/deploy/troubleshooting.md#vip-not-moving-on-failover"

      - alert: GDHCPSplitBrainSuspected
        expr: increase(gdhcp_db_partition_detected_total[10m]) > 0
        labels: {severity: critical}
        annotations:
          summary: "Cluster {{ $labels.cluster_id }} detected a DB partition"
          description: "See docs/deploy/troubleshooting.md#split-brain-symptoms"

      - alert: GDHCPHeartbeatSlow
        expr: histogram_quantile(0.95, rate(gdhcp_heartbeat_write_duration_ms_bucket[5m])) > 500
        for: 5m
        labels: {severity: warning}
        annotations:
          summary: "Server {{ $labels.server_id }} p95 heartbeat write > 500ms"
          description: "Database latency risks spurious failover — check Postgres health."
```

## Application-level health

`GET /api/v1/health` (HTTP, not Prometheus format) returns
`{status, db, ha, version}` and 503 when degraded — cheap to hit from an
external uptime check independent of the Prometheus stack; it's also what
the Docker and Kubernetes healthcheck/liveness probes use.
