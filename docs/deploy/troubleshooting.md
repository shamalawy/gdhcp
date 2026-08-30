# Troubleshooting

Operational runbook for the deployment-layer problems that show up across
bare metal, Docker, and Kubernetes. For DHCP protocol / lease-allocation
bugs, see `docs/design/dhcp-engine.md`; for election/heartbeat internals,
`docs/design/ha-cluster.md`.

## Port 67 busy / bind: address already in use

**Symptom**: `gdhcp` fails to start (or its DHCP listener silently never
comes up) with `listen udp :67: bind: address already in use`.

**Cause**: another DHCP server (isc-dhcp-server, dnsmasq, systemd-networkd's
built-in client in server-adjacent configs, a leftover previous `gdhcp`
process) already holds the port.

**Fix**:
```bash
sudo ss -ulpn | grep :67
sudo systemctl stop isc-dhcp-server dnsmasq 2>/dev/null   # whichever applies
sudo systemctl disable isc-dhcp-server dnsmasq 2>/dev/null
```
In Docker/Kubernetes, this is almost always a second gdhcp container/pod on
the same host — check `docker ps` / `kubectl get pods -o wide` for
duplicates bound to the same node.

## Capability missing: gdhcp starts but can't bind 67 / manage the VIP

**Symptom**: process runs, HTTP API/UI answer fine, but DHCP never responds
and/or the VIP never appears (`permission denied` / `operation not
permitted` in logs around socket bind or netlink calls).

**Bare metal**: check the unit actually has the capabilities:
```bash
systemctl show gdhcp --property=AmbientCapabilities
getcap /usr/local/bin/gdhcp   # setcap fallback, if AmbientCapabilities failed
```
Re-run `sudo ./scripts/install.sh` — it re-applies the setcap fallback on
systemd < 229. On a version installed manually, apply the fallback yourself:
```bash
sudo setcap 'cap_net_admin,cap_net_raw,cap_net_bind_service=+eip' /usr/local/bin/gdhcp
```

**Docker**: confirm `cap_add: [NET_ADMIN, NET_RAW]` is present in
`docker-compose.yml` (already is, by default) and that the image's `setcap`
step succeeded: `docker run --rm --entrypoint getcap gdhcp:latest /usr/local/bin/gdhcp`.

**Kubernetes**: check the pod's `securityContext.capabilities.add` list and
that `allowPrivilegeEscalation: true` is set (capabilities on a non-root
container require it) — both are in `deploy/kubernetes/base/gdhcp/daemonset.yaml`
by default; a PodSecurityPolicy/admission controller in the cluster may be
stripping them, which `kubectl describe pod` surfaces as an admission
rejection, not a runtime error.

## VIP not moving on failover

**Symptom**: the primary node goes down (or is manually failed over via
`gdhcp ha failover`) but clients still can't reach the VIP, or `ip addr`
shows it stuck on the dead node.

**Checklist**:
1. Both nodes are on the **same L2 segment** as `vip_address` — VIP
   failover relies on gratuitous ARP, which does not cross a router. If the
   nodes are in different subnets/VLANs, this is expected to fail; use an
   external load balancer or keepalived with proper VRRP routing instead.
2. `vip_interface` in both nodes' config actually matches a real interface
   name on that host (`ip link show`) — a typo here fails silently at the
   netlink call, not at config load.
3. The new primary actually has `CAP_NET_ADMIN`/`CAP_NET_RAW` (see the
   capability section above) — without it, the election can complete but
   the VIP assignment step fails.
4. Client-side ARP caches: some OSes cache the old MAC for the VIP for
   several seconds even after a correct gratuitous ARP — this is normal and
   not a gdhcp bug; if it's a Kubernetes node's own gratuitous ARP getting
   dropped by a security-hardened switch, that's environmental, not gdhcp.
5. Check `gdhcp ha status` and the `cluster` SSE events on `/api/v1/events`
   for what state each node believes it's in — if both say `PRIMARY`, see
   split-brain below instead.

## Split-brain symptoms (two nodes both think they're primary)

**Symptom**: both nodes answer DHCP simultaneously, `gdhcp ha status` shows
`PRIMARY` on more than one server, or the VIP briefly flickers between
hosts.

**Most common cause**: the database itself is unreachable from one node
while still reachable from the other (a partial network partition), so the
partitioned node can't see the other's heartbeat and calls its own
election — while the still-connected node correctly still holds primary.
gdhcp's election is DB-mediated (heartbeats + advisory state in Postgres),
so **the database being a single point of truth means a DB partition looks
like a peer partition** to the affected node. Check:
```bash
gdhcp ha status --server https://<vip-or-either-node>:8080
# on each node individually, confirm DB reachability:
psql "$GDHCP_DATABASE_DSN" -c 'select 1;'
```
Look for `gdhcp_db_partition_detected_total` incrementing in Prometheus
(see `docs/operations/monitoring.md`) — that metric exists specifically to
flag this condition.

**Fix**: restore DB connectivity to the partitioned node; it should demote
itself once it can see the real primary's heartbeat again. If it doesn't
within `heartbeat_ttl_ms * 2`, manually confirm state with `gdhcp ha
status` and, if genuinely stuck, restart the wrongly-primary node's gdhcp
process — do not simultaneously edit the VIP or DHCP config on both nodes
while split-brained, since config propagation (LISTEN/NOTIFY) is also DB
mediated and may not reach the partitioned node either.

## Health check basics

```bash
curl -sf http://localhost:8080/api/v1/health | jq
# {"status": "ok", "db": "ok", "ha": "primary", "version": "v1.0.0"}
```
`status: degraded` / HTTP 503 with `db: "unreachable"` means Postgres is the
problem, not gdhcp itself — check DSN, network path, and Postgres's own
health before anything else.
