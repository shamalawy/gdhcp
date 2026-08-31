# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Clients that leave the BOOTP broadcast flag clear — ISC dhclient, busybox udhcpc, Windows and macOS, i.e. most of them — never received an OFFER. The reply was addressed to `yiaddr` and written to the UDP socket, so the kernel ARPed for an address the client cannot yet answer for and dropped the datagram. Replies to a client in INIT are now delivered to its hardware address over `AF_PACKET` (amendment A22)
- The lease length written to the database was hardcoded to 24 hours, ignoring both Option 51 and `dhcp.default_lease_time`. The OFFER, the ACK and the stored row could all disagree; leases outlived what the client was told by up to a day. `AllocateLease` now takes the resolved duration (amendment A22)
- Template-level DHCP options never resolved. `orderedTemplateIDs`' `AND $1 <> ''` guard made Postgres cast `''` to `uuid`, so the query failed on every call regardless of its arguments, logging `resolve option failed, skipping` dozens of times per exchange. An absent scope/pool is now passed as NULL (amendment A22)
- The VLAN detail page (`/ipam/vlans/{id}`) rendered an empty box. Its data-loading `effect()` was missing `allowSignalWrites`, so Angular threw `NG0600` before the first request was ever made and the page stayed on a loading skeleton that had no styles of its own (amendment A21)

### Added

- VLANs are now taggable in the UI: editable tag chips and an inline-editable description on the VLAN detail page, and a Tags column on the VLAN list. The server already accepted `entity_type=vlan`; nothing had ever used it (amendment A21)
- The VLAN detail page shows a roll-up of the prefixes on the segment — prefix and subnet counts, total address space, the VRFs involved, and mean DHCP utilization — above the prefix table, which now carries each prefix's tags (amendment A21)
- The shared prefix table gained an opt-in VRF column, used by the VLAN page because one layer-2 segment can be numbered in more than one VRF (amendment A21)

### Changed

- VLANs moved from a button in the IPAM overview's page header to a sub-entry under IPAM in the left sidenav. The prefix tree cannot represent a VLAN, so the section's own navigation had nowhere to put it (amendment A21)
- The VLAN list paginates (25/50/100) instead of loading 200 rows at once; its new Tags column costs one request per row and there is no bulk tag endpoint (amendment A21)

## [0.2.0] - 2026-08-30

### Fixed

- A reservation now outranks an active lease when classifying an address: reserving an address a client is currently using reads as `reserved` immediately. Assigned and Reserved are allocation modes (dynamic vs static), not lifecycle states, so they are mutually exclusive (amendment A20)
- Removed the separate "Pinned" column from the leases and IPAM client tables. A reservation is the same thing the Status column calls "Reserved" — pinning is only the shortcut that creates one — so the second column duplicated the word "Reserved" with a different meaning. The pin remains as a row action (amendment A20)
- The DHCP leases and IPAM clients tables now use the same column order, so the two sections read identically
- Address status now replaces binding state rendering across all surfaces: AddressStatus (free/assigned/reserved/abandoned) folds `released` and `expired` into `free` where the allocator treats them identically (amendment A20)
- IPAM clients listing no longer shows duplicate rows when an address has both a lease and an enabled reservation; the lease row carries the reservation state (amendment A20)
- Subnet utilization now properly accounts for whole-subnet address space: reserved_infra (network/broadcast) + pool_total (addresses in DHCP pools) + static_total (in-subnet but outside pools) = space_total; pool narrowing no longer changes subnet utilization pct (amendment A20)
- Utilization double-count fixed: Available is now authoritative and must never be recomputed from active + reserved in templates (amendment A20)
- Client listing pagination now includes stable `id` tiebreaker on every sort to prevent row repetition across pages (amendment A20)
- Disabled reservations are now visible in IPAM clients listing so operators can see and re-enable them (amendment A20)
- CLI `lease list` and `prefix clients` commands now show `STATUS` column (AddressStatus) instead of raw binding state (amendment A20)
- `GET /leases` and `GET /prefixes/{id}/clients` accept a `status` filter (free/assigned/reserved/abandoned) whose SQL predicates are kept in lockstep with `models.ClassifyAddressStatus` (amendment A20)
- Reservation state is now visible in lease reads: DHCP and IPAM leases tables show whether a client is pinned, and with which address if pinned elsewhere (amendment A19)
- CLI `lease` command now shows a `PINNED` column in the leases table (amendment A19)
- Store now rejects duplicate reservations on the same address in a pool with `ErrReservationIPTaken` (amendment A19)
- `PUT /reservations/{id}` now accepts optional `reserved_ip` field and treats absent fields as "leave alone", fixing enable/disable toggles that were erasing hostname and description (amendment A19)
- Error messages for reservation conflicts are now sentences, not Postgres constraint names (amendment A19)

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
