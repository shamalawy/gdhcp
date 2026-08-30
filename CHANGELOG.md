# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-08-30

Initial public release of gdhcp: a high-availability DHCP server with integrated IPAM, REST API, CLI, and Angular 18 web UI.

### Added

- **DHCPv4 engine**: DISCOVER/OFFER/REQUEST/ACK, relay agent support (option 82), per-VRF interface binding, atomic lease allocation under concurrent load, reservation matching by seven client identifier types (MAC, client-ID, hostname, circuit-ID, remote-ID, vendor class, user class) with deterministic precedence
- **IPAM**: hierarchical prefix tree (aggregate → container → subnet), utilization tracking, tag/label support, global search, DHCP client rollup per-prefix showing leases and reservations from the prefix and all descendants
- **HA clustering**: multi-node clusters with heartbeat and election stored in PostgreSQL, VIP failover via netlink and gratuitous ARP, manual failover from CLI/API
- **Option system**: full IANA DHCPv4 option catalog (codes 1–254) with inheritance across global → template → scope → pool → reservation
- **REST API**: snake_case JSON, JWT session auth, long-lived API tokens, RBAC (admin/operator/viewer), SSE endpoint for live dashboard/lease/cluster updates
- **Web UI**: Angular 18 standalone components with signals — dashboard, IPAM tree view, DHCP (scopes/pools/leases/reservations/templates), HA status, admin (users/tokens) sections
- **Lease management**: lease actions (pin/reserve, force renew, release, abandon, delete) via API and web UI, including bulk operations
- **DHCP clients page**: global search across all leases by IP/MAC/hostname with bulk-apply actions
- **CLI**: single `gdhcp` binary with cobra subcommands (serve, migrate, version, vrf, prefix, scope, pool, lease, cluster, ha, user, search, etc.)
- **Metrics**: Prometheus endpoint on separate port with dedicated endpoint at /metrics
- **Deployment**: Docker Compose, bare-metal systemd, and Kubernetes (Kustomize) deployment templates
- **VLANs**: layer-2 segmentation support with VLAN groups and per-subnet VLAN assignment
- **Default admin credential**: admin/admin seed in migration 0003_seed_admin.sql for first login; no in-product password change yet (see Security section of README)

### Fixed

- Global search now correctly returns empty slice on no results (was null, causing client throw)
- Global search coverage expanded to include VLANs, VLAN groups, options, and leases with hostname/MAC/IP search
- Lease client identifiers now stored canonically with type prefix (mac:, client_id:, hostname:, etc.), fixing reservation matching for all write paths
- Binding keys are validated and canonicalized on all mutations via models.IsCanonicalBindingKey

### Notes

- Pure Go build (CGO_ENABLED=0) with no C dependencies; static binary with pgx driver
- Web assets embedded via go:embed; placeholder web/dist/index.html committed for fresh clones
- All configuration via YAML with environment variable override support (GDHCP_<SECTION>_<FIELD>)
- Default admin/admin credential cannot be changed in-product yet; network isolation recommended
- This is a pre-1.0 release; API and database schema may change before 1.0
