# gdhcp

High-availability DHCP server with integrated IPAM, single binary, Postgres-backed, zero local state.

gdhcp is a DHCPv4 server and IP address management platform for infrastructure that needs more than one DHCP node. It combines a full DHCPv4 engine, a REST API, an embedded web UI, and a CLI client in one static binary. Built on PostgreSQL as the single source of truth, gdhcp eliminates the operational complexity of distributed DHCP cluster state: no local lease files, no keepalived or VRRP daemons, no eventual consistency between nodes.

This is pre-1.0 software (v0.1.0). It is feature-complete and covered by tests, but it has no production mileage yet, and the REST API and database schema may change before 1.0.

## Status

[![CI](https://github.com/shamalawy/gdhcp/actions/workflows/ci.yaml/badge.svg)](https://github.com/shamalawy/gdhcp/actions/workflows/ci.yaml)
[![Latest Release](https://img.shields.io/github/v/release/shamalawy/gdhcp)](https://github.com/shamalawy/gdhcp/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.26+](https://img.shields.io/badge/go-1.26+-00add8.svg)](https://golang.org/doc/devel/release#go1.26)

## Features

| Component | Capabilities |
|---|---|
| **DHCPv4 engine** | DISCOVER/OFFER/REQUEST/ACK, relay agent (option 82) support, per-VRF interface binding, atomic concurrent-safe lease allocation, reservation matching by seven client identifier types (MAC, client-ID, hostname, circuit-ID, remote-ID, vendor class, user class) |
| **IPAM** | Hierarchical prefix tree (aggregate → container → subnet), utilization tracking, tags and labels, global search, DHCP client rollup showing leases and reservations |
| **Option system** | Full IANA DHCPv4 option catalog (codes 1–254) with inheritance: global → template → scope → pool → reservation |
| **HA clustering** | Multi-node clusters with Postgres-backed heartbeat and leader election, VIP failover via netlink and gratuitous ARP, manual failover over CLI/API |
| **REST API** | JWT session auth, long-lived API tokens, RBAC (admin/operator/viewer), snake_case JSON, live updates via SSE |
| **Web UI** | Angular 18 standalone components, dashboard with utilization and lease stats, IPAM tree, DHCP scopes/pools/leases/reservations, HA status, user and token management. Bulk lease actions: pin (reserve), force renew, release, abandon, delete. Global search across all objects. |
| **CLI** | Full-featured client over HTTP; same binary as the server. Subcommands: `vrf`, `prefix`, `scope`, `pool`, `lease`, `cluster`, `ha`, `user`, `search`, and more. |
| **Metrics** | Prometheus endpoint on a separate port; DHCP transaction counters, lease allocation contention, HA heartbeat/election latency, VIP failures, and Go/process/DB pool metrics. |

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design. The file [docs/AMENDMENTS.md](docs/AMENDMENTS.md) contains binding corrections to all documentation — where it conflicts with any other doc, it wins.

## Quickstart

The fastest way to try gdhcp is Docker Compose, which runs the server and PostgreSQL together on your local machine.

```bash
git clone https://github.com/shamalawy/gdhcp.git
cd gdhcp

cp deploy/docker/config.yaml.example deploy/docker/config.yaml
# Edit deploy/docker/config.yaml to review settings:
# - dhcp.interfaces starts empty (API/UI-only mode); add interface bindings to enable DHCP
# - server.jwt_secret is randomly generated at startup; set an explicit value for multi-node clusters

docker compose -f deploy/docker/docker-compose.yml up -d --build
docker compose -f deploy/docker/docker-compose.yml logs -f gdhcp_server
curl http://localhost:8080/api/v1/health
```

Then visit **http://localhost:8080** in your browser.

**Login**: username `admin`, password `admin`. See [First login security note](#security-note-default-credentials) below.

The Docker stack uses `network_mode: host` for real DHCP broadcast traffic and `CAP_NET_ADMIN`/`CAP_NET_RAW` for VIP failover. Both are already configured in `deploy/docker/docker-compose.yml`. For a two-node HA cluster in Docker, see [docs/deploy/docker.md](docs/deploy/docker.md).

## Install

Beyond Docker Compose, gdhcp is available via:

| Platform | Method | Details |
|---|---|---|
| **Linux** | Tarball (.tar.gz) | Pre-built releases for amd64 and arm64 at [GitHub releases](https://github.com/shamalawy/gdhcp/releases). Includes SHA256 checksums. |
| **Linux** | Debian/RPM packages | `.deb` and `.rpm` packages for easy integration with package managers (see releases). |
| **Any** | `go install` | `go install github.com/shamalawy/gdhcp/server/cmd/gdhcp@latest` (embeds a placeholder UI; rebuild with `make web` for the full Angular UI if needed) |
| **Kubernetes** | Kustomize | Overlays for development and production at `deploy/kubernetes/{base,overlays}`. See [docs/deploy/kubernetes.md](docs/deploy/kubernetes.md) for setup. |
| **Any** | Build from source | Requires Go 1.26 and Node 20. Run `make build` to produce `dist/gdhcp`. See [docs/deploy/bare-metal.md](docs/deploy/bare-metal.md) for systemd integration. |

## Screenshots

<!-- Capture the following screens and save to docs/images/:
- dashboard.png: web UI dashboard tab, showing cluster status, lease utilization, transaction rates
- ipam-tree.png: IPAM tab with VRF/prefix/subnet tree and utilization bars
- dhcp-leases.png: DHCP Leases tab with search, bulk actions, and reservation pins
-->

Not captured yet. The screens worth showing:

- **Dashboard**: cluster status, lease utilization, transaction rates
- **IPAM**: hierarchical prefix tree with utilization per prefix
- **DHCP Leases**: search, filtering, bulk actions (pin/renew/release)

## Documentation

Start here, in order:

- **[docs/MANUAL.md](docs/MANUAL.md)** — Operator manual with installation, configuration, HA setup, monitoring, backup, troubleshooting (start here if you're deploying gdhcp).
- **[docs/api/endpoints.md](docs/api/endpoints.md)** — Complete REST API reference with request/response examples.
- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — System design, data model, HA cluster mechanics, and subsystem architecture.
- **[docs/AMENDMENTS.md](docs/AMENDMENTS.md)** — Binding corrections to all documentation; read if you spot conflicts between docs.
- **[CONTRIBUTING.md](CONTRIBUTING.md)** — How to build, test, and submit changes.
- **[SECURITY.md](SECURITY.md)** — Security model, default credentials, and hardening guide.

For deep dives: [docs/design/](docs/design) (frontend, lease allocation, IPAM, HA), [docs/deploy/](docs/deploy) (Docker, bare metal, Kubernetes, troubleshooting), [docs/operations/](docs/operations) (monitoring, backup/recovery, performance tuning).

## Security note: default credentials

Migration `0003_seed_admin.sql` seeds a default account on fresh databases:

- **Username**: `admin`
- **Password**: `admin`

This credential is well known, and it is live the moment `server.api_addr` is reachable.

There is no password-change endpoint yet — `PUT /api/v1/users/{id}` deliberately does not
accept a password field. Until one exists, replace the seeded account instead of trying to
edit it:

```bash
# 1. log in as admin/admin and create a real admin account (password >= 8 chars)
curl -sX POST http://localhost:8080/api/v1/users \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"username":"you","email":"you@example.com","password":"<strong-password>","role":"admin","is_active":true}'

# 2. log in as the new account, then disable the seeded one
#    (username and email are required by the update handler even when unchanged)
curl -sX PUT http://localhost:8080/api/v1/users/1 \
  -H "Authorization: Bearer $NEW_TOKEN" -H 'Content-Type: application/json' \
  -d '{"username":"admin","email":"admin@localhost","role":"admin","is_active":false}'
```

Do this before exposing the API, and firewall `server.api_addr` to trusted hosts regardless.
See [SECURITY.md](SECURITY.md) for the full security model and hardening checklist.

## License

MIT. See [LICENSE](LICENSE) for details.
