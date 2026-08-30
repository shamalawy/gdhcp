# Bare-Metal Deployment

Installs gdhcp as a systemd service directly on a Linux host. This is the
recommended path for real DHCP serving — it has direct access to host
network interfaces without the broadcast/VIP caveats that containers and
Kubernetes carry (see [docker.md](docker.md) and [kubernetes.md](kubernetes.md)).

## Requirements

- Linux with systemd (kernel with `CAP_NET_ADMIN`/`CAP_NET_RAW`/`CAP_NET_BIND_SERVICE` support — any modern distro)
- A reachable PostgreSQL 13+ server (local or remote)
- `sudo`/root on the target host

## Install flow

```bash
# From a machine with the repo + Node 20 + Go 1.26 (or download a release
# tarball and skip straight to bootstrap-db.sh + install.sh):
make build                       # -> dist/gdhcp

sudo ./scripts/bootstrap-db.sh   # creates the gdhcp role + database
sudo ./scripts/install.sh        # installs binary, config, systemd unit

sudo systemctl edit gdhcp        # optional: env overrides, or edit
                                  # /etc/gdhcp/config.yaml directly

sudo gdhcp migrate --config-file /etc/gdhcp/config.yaml
sudo systemctl start gdhcp
systemctl status gdhcp
journalctl -u gdhcp -f
```

`install.sh` also accepts a pre-built release tarball instead of a local
`dist/gdhcp`:

```bash
sudo GDHCP_RELEASE_URL=https://github.com/shamalawy/gdhcp/releases/download/v1.0.0/gdhcp-v1.0.0-linux-amd64.tar.gz \
    ./scripts/install.sh
```

What it does:
1. Creates the `gdhcp` system user (no login, no home shell)
2. Creates `/etc/gdhcp`, `/var/lib/gdhcp`, `/var/log/gdhcp` (mode 750, owned by `gdhcp`)
3. Installs the binary at `/usr/local/bin/gdhcp`
4. Writes `/etc/gdhcp/config.yaml` from `config.yaml.example` (prompts for the DB DSN), unless one already exists
5. Installs `deploy/systemd/gdhcp.service` and `gdhcp.env`, applies a `setcap` fallback on systemd versions that predate `AmbientCapabilities` (pre-229), enables the service

After first login, **change the default admin password** — migration
`0003_seed_admin.sql` seeds `admin`/`admin` so there is *something* to log
in with; leaving it as-is is a live credential on your network.

To remove: `sudo ./scripts/uninstall.sh` (add `--purge` to also delete
config/data/logs and the `gdhcp` user; the database itself is never touched
by either script).

## HA pair walkthrough (two nodes + VIP)

Two bare-metal servers sharing one PostgreSQL database and a floating
virtual IP (VIP) that DHCP clients are pointed at. See
`docs/design/ha-cluster.md` for the election/heartbeat mechanics; this is
just the config wiring.

**node1** (`/etc/gdhcp/config.yaml`, after `install.sh`):

```yaml
server:
  api_addr: "0.0.0.0:8080"
  jwt_secret: "<same random 32+ byte value on both nodes>"

database:
  dsn: "postgres://gdhcp:changeme@10.0.0.5:5432/gdhcp?sslmode=disable"  # shared DB host

dhcp:
  interfaces:
    - name: "eth0"
      vrf_id: "default"

ha:
  enabled: true
  cluster_id: "prod-cluster"
  server_id: "dhcp-01"
  server_name: "node1"
  node_id: 1
  vip_address: "192.168.1.254/24"
  vip_interface: "eth0"
  heartbeat_interval_ms: 1000
  heartbeat_ttl_ms: 3000
  election_interval_ms: 2000
```

**node2**: identical, except `server_id: "dhcp-02"`, `server_name: "node2"`,
`node_id: 2`. `jwt_secret` must be **the same value** on both nodes —
otherwise a session created against whichever node currently holds the VIP
is rejected if a request lands on the other one.

Both nodes must:
- Be on the same L2 segment as `vip_address` (VIP failover uses gratuitous
  ARP, which does not cross routed L3 boundaries)
- Reach the same PostgreSQL instance (`database.dsn`) — the DB, not gdhcp
  itself, is the source of truth for cluster state
- Have `eth0` (or whatever `vip_interface`/`dhcp.interfaces[].name` names)
  be the actual DHCP-serving interface on that host

Point DHCP clients (or your relay agent's `giaddr` config) at the VIP
address, not either node's real IP — that's what makes failover invisible
to clients.

See [troubleshooting.md](troubleshooting.md) if the VIP doesn't move on
failover, or both nodes think they're primary.
