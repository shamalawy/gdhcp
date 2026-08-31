# AMENDMENTS.md — Binding overrides to the architecture contract

This file resolves contradictions found in ARCHITECTURE.md / CONTRACTS.md / FILETREE.md.
**Where this file conflicts with any other doc, THIS FILE WINS.**

## A1. JSON casing: snake_case everywhere
All API request/response JSON uses `snake_case` (as in `docs/api/endpoints.md` examples and
the model struct tags in CONTRACTS.md Part 1). The "camelCase" section in CONTRACTS.md
Part 2 is **void**. Frontend TypeScript models use snake_case fields to match.

## A2. One binary only: `gdhcp`
There is NO `gdhcp-cli` binary. `server/cmd/gdhcp-cli` must not exist. The single binary
`gdhcp` (from `server/cmd/gdhcp`) provides everything via cobra subcommands:
- `gdhcp serve` — run the server (DHCP + API + embedded UI)
- `gdhcp migrate` — run DB migrations and exit
- `gdhcp version`
- API-client commands (talk to the REST API over HTTP using `--server`/`--token`,
  env `GDHCP_SERVER`/`GDHCP_TOKEN`, or `~/.gdhcp.yaml` written by `gdhcp login`):
  `vrf`, `prefix`, `tree`, `scope`, `pool` (reservations live under pool per product rule),
  `lease`, `template`, `option`, `tag`, `cluster`, `user`, `search`, `ha` (status/failover).
Build output: `dist/gdhcp` only.

## A3. Pure Go, no CGO
PostgreSQL driver is `github.com/jackc/pgx/v5` (+ `pgxpool`). Build with `CGO_ENABLED=0`;
the binary is fully static. Any "CGO required" statement elsewhere is **void**.

## A4. go:embed — no `..` paths (they are illegal)
The `//go:embed ../../../web/dist/*` snippets in FILETREE.md are **void** (go:embed cannot
reference parent directories). Binding layout:
- Web assets: `web/embed.go` — `package web`, `//go:embed all:dist`, `var DistFS embed.FS`,
  plus helper `func Assets() (fs.FS, error)` that returns `fs.Sub(DistFS, "dist/browser")`
  if that subdir exists, else `fs.Sub(DistFS, "dist")`. A committed placeholder
  `web/dist/index.html` keeps `go build` working before the Angular build runs.
  The HTTP server serves this FS at `/` with SPA fallback to `index.html` for any
  non-`/api`, non-file path.
- Migrations: `server/internal/db/migrations/NNNN_*.sql`, embedded from within package db:
  `//go:embed migrations/*.sql` in `server/internal/db/embed.go`.

## A5. Schema fixes (apply when creating migration 0001 from docs/db/schema.sql)
1. `dhcp_leases.binding_server_id`: **drop NOT NULL** (it has `ON DELETE SET NULL`,
   which is contradictory with NOT NULL).
2. All GiST usage on `cidr`/`inet` columns must name the opclass `inet_ops`:
   - `EXCLUDE USING gist (vrf_id WITH =, prefix inet_ops WITH &&) WHERE (prefix_type = 'subnet')`
   - `CREATE INDEX idx_ipam_prefixes_prefix_gist ON ipam_prefixes USING gist (prefix inet_ops);`
   Keep `CREATE EXTENSION IF NOT EXISTS btree_gist;` (needed for `vrf_id WITH =`).
3. Migration 0003 seeds a default admin user: username `admin`, password `admin`
   (bcrypt hash), `is_active = true`, with description "Default admin — change this password".
4. Migration 0002 seeds the FULL IANA DHCPv4 option catalog (all assigned codes 1–254,
   correct value_type, human-readable description, RFC reference) — not just the 11 sample
   rows shown in schema.sql section 11. Add value_type `'fqdn'` and `'route_list'`
   (classless static routes, option 121) to the CHECK constraint in 0001.

## A6. Lease allocation model
The `state = 'free'` pre-materialized-rows pseudocode in CONTRACTS.md Part 7 is **void**
(the schema has no free rows — leases exist only once allocated). Binding approach:
1. If a reservation matches the client in a pool serving the request → offer/ack that IP.
2. Else prefer the client's existing active/most-recent lease in the pool (renew affinity).
3. Else pick candidate IPs inside the pool range that have no active lease and no
   reservation and are not network/broadcast addresses (SQL against `generate_series`
   over the range or an equivalent scan, a few candidates at a time), then claim with
   `INSERT ... ON CONFLICT (pool_id, ip_address) DO NOTHING` (upsert-reuse if an
   expired/released row exists for that IP). Zero rows affected → another server won →
   try next candidate. Retry ≤3 rounds; then NAK/no-offer and record `no_leases`.
   READ COMMITTED isolation is sufficient — atomicity comes from the unique constraint.

## A7. Logging & metrics
Use stdlib `log/slog` (JSON or text per config), NOT logrus. OpenTelemetry tracing is
deferred (do not implement `logger/tracing.go`). Prometheus metrics stay as specified
(`prometheus/client_golang`), exposed on `metrics.listen_addr` at `/metrics`.

## A8. Fixed library choices (with import paths)
- DHCP: `github.com/insomniacslk/dhcp/dhcpv4` (+ `dhcpv4/server4` for listeners)
- Netlink (VIP): `github.com/vishvananda/netlink`
- Gratuitous ARP: `github.com/mdlayher/arp` (note spelling; "mdlayler" in docs is a typo)
- HTTP router: `github.com/gorilla/mux`
- DB: `github.com/jackc/pgx/v5` / `pgxpool`
- UUID: `github.com/google/uuid`; JWT: `github.com/golang-jwt/jwt/v5`
- Passwords/tokens: `golang.org/x/crypto/bcrypt`; YAML: `gopkg.in/yaml.v3`
- CLI: `github.com/spf13/cobra`

## A9. Frontend stack
Angular 18 with **standalone components** (no NgModules — `app.module.ts` entries in
FILETREE.md are void; use `app.routes.ts` + feature `*.routes.ts` files), signals for
state, Angular Material 18. No chart library: utilization bars/gauges and history
sparklines are hand-rolled SVG/CSS components. `ng build` output must land in `web/dist/`
such that `index.html` sits at `web/dist/index.html` or `web/dist/browser/index.html`
(both are handled by `web.Assets()`).

## A10. Decoupling rule for parallel implementation
Packages must not import sibling subsystems being built in parallel. Where the API layer
needs HA or DHCP engine behavior (cluster health, manual failover, live stats), it defines
small consumer-side interfaces in its own package (e.g. `api.HAStatus`, `api.EngineStats`)
against `models` types; `server/internal/server/app.go` (written by the integration agent)
wires the concrete implementations in. Same pattern for the DHCP engine's view of HA
(`dhcp.ServingGate` — whether this node should answer).

## A11. Config additions
`server.jwt_secret` (string; if empty a random secret is generated at startup and sessions
reset on restart). `dhcp.interfaces: []` (empty list) is valid and means API/UI-only mode
(no DHCP listeners) — used for IPAM-only nodes and local development/smoke tests.
`ha.enabled: false` means standalone single-server mode (still registers in DB).

## A12. SSE for live updates
One endpoint `GET /api/v1/events` (Server-Sent Events): emits `stats` (dashboard rollup,
every ~10s), `leases` (lease churn per scope), and `cluster` (role/heartbeat changes)
events. The frontend dashboard subscribes; polling fallback is acceptable in v1.

## A14. API token format & hashing
Bcrypt cannot be used for token lookup (no deterministic hash). Binding: raw tokens are
`gdhcp_<random 32 bytes base64url>`; `api_tokens.token_hash` stores the **SHA-256 hex**
of the raw token (unique index enables direct lookup). The raw token is returned exactly
once at creation. Any "bcrypt hash of token" statement elsewhere is **void** (bcrypt
remains for user passwords).

## A15. RBAC role column
`users` gets `role VARCHAR(20) NOT NULL DEFAULT 'admin' CHECK (role IN ('admin','operator','viewer'))`
in migration 0001 (the schema.sql draft omitted it). API tokens inherit their user's role.
admin: everything; operator: all CRUD except users/tokens/cluster config; viewer: read-only.

## A13. Store interfaces
`server/internal/store/interface.go` (written by the Foundation unit) is the compiled
source of truth; CONTRACTS.md Part 1 is its blueprint but type references must compile
against `internal/models` (e.g. `*models.DHCPLease`). Signature adjustments needed to
compile (package qualifiers, `uuid.UUID` vs `string` IDs — use `string` UUIDs throughout)
are allowed and expected; names and semantics must be preserved.

## A16. VLANs, prefix utilization semantics, and IPAM navigation
Added after the initial build; these extend (not void) the sections above.

### A16.1 VLAN subsystem
Layer-2 segmentation was absent — VRFs model layer-3 routing isolation and nothing
recorded which VLAN a subnet lives on. Migration `0004_vlans.sql` adds:
- `vlan_groups` — a named layer-2 domain, optionally scoped to a VRF, with a
  `min_vid`/`max_vid` range (both within 1–4094).
- `vlans` — `vid` (1–4094; 0 and 4095 are reserved by 802.1Q), `name`, `status`
  (`active`/`reserved`/`deprecated`), optional `group_id`. A VID is unique **within
  its group**, and unique among **ungrouped** VLANs — two partial unique indexes,
  since `UNIQUE(group_id, vid)` would not constrain rows where `group_id IS NULL`.
- `ipam_prefixes.vlan_id` — `ON DELETE SET NULL`, so deleting a VLAN unassigns
  prefixes rather than deleting address space.

REST: `/vlans` and `/vlan-groups` CRUD plus `GET /vlans/{id}/prefixes`; writes are
`RoleOperator` and audited under entity types `vlan` / `vlan_group`.
A2's CLI verb list gains **`vlan`** (with a `vlan group` subcommand).

### A16.2 `prefix` and `vlan` are taggable
`TaggableEntityType` gains `prefix` and `vlan`. `prefix` was already used by the
frontend but rejected by the server's `validEntityType`, so prefix tagging silently
failed. `entity_tags.entity_type` has no CHECK constraint, so no migration is needed
beyond the column comment.

### A16.3 Prefix utilization has two views
`GET /prefixes/{id}/utilization` returns `models.PrefixUtilization`, because a single
number cannot describe an aggregate:
- **DHCP view** (`total`/`active`/`reserved`/`available`/`pct`/`scope_count`) — lease
  usage summed over every scope on this prefix **or any descendant**, walked via
  `parent_id`. Same shape as `/scopes/{id}/utilization`.
- **Address-space view** (`space_total`/`space_allocated`/`space_pct`/
  `child_prefix_count`) — how much of the prefix's own CIDR its **direct** children
  cover. Defined for every prefix, including those with no DHCP anywhere beneath.

`stats_utilization_samples.entity_type` gains `prefix`; `SampleUtilization` now
records a sample for **every** prefix and writes the cached
`ipam_prefixes.utilization_percentage` from the DHCP rollup (NULL only when no scope
exists in the subtree). `GET /ipam/tree` additionally returns `space_pct` per node so
aggregates and containers in a pure-IPAM deployment still render a bar.

Two endpoints the frontend already called were never registered and returned 404:
`GET /ipam/next-available-subnet` (the store method existed) and
`GET /prefixes/{id}/utilization`. Both are now mounted, along with
`GET /prefixes/{id}/ancestors` (root-first, for breadcrumbs and tree reveal).

### A16.4 Navigation
- The IPAM tree moved out of the app sidenav into a split layout at `/ipam`
  (`ipam/layout/ipam-shell.component.ts`): resizable, collapsible, full height, with
  prefix-type chips, a hierarchy legend, VLAN chips and utilization bars per node.
  Clicking a node with a scope opens its **prefix** page (which links on to the
  scope) instead of leaving the IPAM section.
- `/dhcp` nests under a tab strip (Scopes | Templates | Options). The template and
  option-catalogue pages already existed but nothing linked to them.
- `docs/design/frontend.md:36,685` describes a 5-level tree
  (`vrf → aggregate → subnet → scope → pool`); the implementation never followed it
  and `GET /ipam/tree` returns prefix nodes only. That doc text is **stale**.

## A17. Client identity, reservations, and DHCP clients in IPAM
Added after A16. Two defects and two gaps are settled here.

### A17.1 The binding-key prefix defect
Reservations are matched by `dhcp_reservations.binding_key`, which the allocation path
builds from the incoming packet as `"<type>:<value>"`. Every write path — REST API, web
UI and CLI — stored the operator's value **bare** (`aa:bb:cc:dd:ee:ff`) instead, so no
reservation created through the product ever matched. Nothing failed loudly: the client
simply received a dynamic address. All three reservations in the demo database were
affected.

Binding keys are therefore **always** stored canonically, and `models.BuildBindingKey`
is the only sanctioned way to construct one. `models.IsCanonicalBindingKey` is the
store-side gate: a key that is not both correctly prefixed *and* in canonical value form
is rejected with `ErrInvalidInput` rather than written. Migration `0005_client_identifiers.sql`
repairs rows already on disk (it is idempotent — a row is only rewritten when it does not
already carry its own type prefix) and lowercases MAC keys, since
`net.HardwareAddr.String()` is lowercase and an upper-case key misses for exactly the
same reason a bare one did.

Note the "is this key already prefixed?" test must look for a **known type prefix**, not
merely for a colon: a bare MAC contains five colons, and testing for a colon made the API
reject raw MAC addresses outright.

### A17.2 Seven binding types
`hostname` previously existed in the enum and the CHECK constraint but was dead — the
engine never read option 12, so such a reservation could never fire. Rather than remove
it, the engine now reads the options needed to honour it, and four further identifiers
join it. `relay.go` already parsed option 82's circuit and remote IDs for logging; they
are threaded through to the allocation path rather than re-parsed.

| Type | Source | Canonical value form |
|---|---|---|
| `mac` | packet chaddr | lowercase colon form, `mac:aa:bb:cc:dd:ee:ff` |
| `client_id` | option 61 | lowercase hex, `client_id:01aabbccddee` |
| `hostname` | option 12 | trimmed, lowercased, `hostname:printer-hq-1` |
| `circuit_id` | option 82 sub-option 1 | lowercase hex, `circuit_id:65746820312f302f3234` |
| `remote_id` | option 82 sub-option 2 | lowercase hex, `remote_id:dead` |
| `vendor_class` | option 60 | lowercase hex, `vendor_class:4d53465420352e30` |
| `user_class` | option 77 | lowercase hex, `user_class:69505845` |

The five opaque types are stored as hex because their wire values are arbitrary bytes.
`models.CanonicalBindingValue` accepts **either** form from an operator: a value that
already looks like hex (an even number of hex digits) is taken as hex and lowercased;
anything else is hex-encoded as its UTF-8 bytes, so `circuit_id` can be typed as
`eth 1/0/24`. This is ambiguous for short even-length input (`1234` is both), so the web
UI offers an explicit Text/Hex mode and sends hex, which round-trips unchanged.

Migration 0005 widens `valid_binding_type` to all seven values.

### A17.3 Match precedence
When a client's identity matches several reservations in a pool, the first type in
`models.BindingTypePrecedence` wins:

`client_id`, `mac`, `circuit_id`, `remote_id`, `vendor_class`, `user_class`, `hostname`.

An explicit client identifier (RFC 2131 / RFC 4361) is the client's own stable identity
and outranks a hardware address, which can move between NICs. Relay-inserted identifiers
are asserted by trusted network equipment and come next. Class identifiers describe a
*kind* of device rather than one device, so they are weaker. A hostname is asserted by
the client itself, is trivially spoofed and is not unique, so it must never outrank a
hardware or identifier match.

The ordering must be deterministic and identical in Postgres and in the mock. Since
`BindingKeys()` already emits its candidates in precedence order, Postgres orders the
matches by `array_position($2, binding_key)` — the candidate's position in that same
array — and the mock walks the list in order. `ORDER BY binding_key` is **void**: it
ordered alphabetically, not by specificity.

### A17.4 ClientIdentity on the allocation path
`store.AllocateLease` and `store.GetLeaseByClient` take a `models.ClientIdentity` — the
seven canonicalised identifier fields plus `BindingKeys()`, which yields the candidate
keys in precedence order — instead of the `(clientMAC, clientID)` string pair. Threading
seven identifiers positionally would be unreadable and every future identifier would
break the signature again. The engine builds exactly one `ClientIdentity` per packet, so
DISCOVER and REQUEST cannot derive different identities for the same client.

### A17.5 Leases capture the client hostname
`dhcp_leases.client_hostname` existed but was never populated from traffic, because the
engine did not read option 12. `AllocateLease` now takes the captured hostname and
persists it.

### A17.6 GET /prefixes/{prefix_id}/clients
A read endpoint returning a paginated union of leases and enabled reservations for a
prefix **and every descendant prefix**, walking `parent_id` with a recursive CTE, the
same descent the utilization rollup uses (A16.3). This is what lets the IPAM section
answer "who is actually on this address space?" for a container or aggregate without the
operator walking down to each pool.

Items are `models.DHCPClient`: `id`, `kind` (`lease`|`reservation`), `ip_address`,
`pool_id`, `pool_name`, `scope_id`, `scope_name`, `prefix_id`, `client_mac`, `client_id`,
`client_hostname`, `binding_state`, `lease_start`, `lease_end`, `binding_key`,
`binding_type`, `is_enabled`, `created_at`, `updated_at`. Fields belonging to only one
kind are null for the other: a reservation has no lease timing or binding state, a lease
has no binding key. A MAC-bound reservation reports its address in `client_mac` so one
table can render both kinds.

Because the two branches of the UNION must agree on column type, every NULL placeholder
is cast explicitly — an untyped NULL takes the first branch's type and silently mistypes
the column. The reservation branch only casts `binding_key` to `macaddr` when it matches
a MAC pattern, so a malformed legacy row cannot abort the whole query.

Filters: `kind`, `state` (lease binding state; reservations have none, so a state filter
excludes them), and free-text `q` over IP / MAC / hostname. Sorts: `ip_address` (default),
`lease_end`, `client_mac`, `client_hostname`. The total is a separate count query, so an
empty page still reports the true total.

### A17.7 Reservation entry points
Creating a reservation previously required drilling from the scope to a pool to its
Reservations tab, which is why the feature read as missing. It is now reachable from:
- the IPAM prefix page's DHCP clients table — a per-row Reserve action on a lease,
  prefilled from that client's identifiers;
- the pool Leases tab — the same per-row action;
- a new scope-level Reservations tab aggregating every pool in the scope, with an Add
  button that asks which pool when the scope has more than one.

A2's CLI verb list gains `prefix clients`.

## A18. Lease management, global search correctness, and shell navigation
Added after A17. Leases stop being a read-only resource, `GET /search` becomes usable,
and the app shell's wordmark moves.

### A18.1 Leases are operator-writable
`docs/api/endpoints.md`'s "Lease Endpoints (Read-Only)" heading is **void** for these
five routes; the listing/lookup routes remain read-only. New, all `RoleOperator`, all
audited under entity type `lease`:

| Route | Effect |
| --- | --- |
| `POST /leases/{lease_id}/release` | `binding_state → released`; the address returns to the pool. Audited as `lease_release`. |
| `POST /leases/{lease_id}/abandon` | `binding_state → abandoned`; held back from reallocation until the reaper sweeps it. |
| `POST /leases/{lease_id}/renew` | Force renew. Optional body `{"duration_seconds": N}`; `0`/absent reuses the lease's own `lease_end - lease_start`, falling back to 24h. |
| `DELETE /leases/{lease_id}` | Removes the lease row. |
| `POST /leases/bulk` | `{"action": "release"\|"abandon"\|"renew"\|"delete", "lease_ids": [...], "duration_seconds": N}` — at most 500 ids. |

Release and abandon reuse the engine's `LeaseStore` methods; the handler loads the lease
first and passes back its own recorded MAC, so an operator never supplies an identifier
the server already holds.

**Force renew is not `LeaseStore.RenewLease`.** That method is the client-driven path: it
requires a matching MAC and an already-`active` lease. `LeaseAdminStore.ForceRenewLease`
skips both and reactivates a lease that had expired, been released or been abandoned.
That is safe because `UNIQUE (pool_id, ip_address)` means the row *is* that address —
there is no second row another client could be holding it through.

**Bulk is not transactional, by design.** It runs N independent store calls and answers
`200` with `{action, succeeded, failed, results: [{lease_id, ok, error}]}`. Releasing 40
leases where one has meanwhile been deleted must release the other 39 and say so, not
roll back. Per-lease error strings are deliberately generic; unclassified failures are
logged server-side, since the handler answers 200 and never reaches `RespondError`.

`LeaseAdminStore` (new, in `store/interface.go`) holds `DeleteLease` and
`ForceRenewLease` — the two mutations with no counterpart on the allocation path. Both
adjust the pool's cached active-lease count when the state they change crosses
`active`.

`ListLeases` additionally honours `ListParams.Search`, matched case-insensitively
against the lease's IP text, MAC text and client hostname at once, and sorts on
`client_mac`, `client_hostname`, `binding_state` and `updated_at` as well as the
original three.

A2's CLI verb list gains `lease release|abandon|renew|delete`, each accepting several
lease IDs (more than one routes through `/leases/bulk`).

### A18.2 Global search: URLs, coverage, and the empty result
Three defects made `GET /search` read as broken.

1. **Every URL 404'd in the SPA.** The SQL emitted `/vrfs/<id>`, `/scopes/<id>`,
   `/pools/<id>`, `/leases/<id>`, `/reservations/<id>`, `/templates/<id>`,
   `/clusters/<id>` — none of which the Angular router mounts (only `/ipam/prefixes/<id>`
   happened to be right). **Binding: a search result's `url` must resolve in
   `web/src/app/**/*.routes.ts`.** The mapping is now:

   | Type | URL |
   | --- | --- |
   | `vrf` | `/ipam/vrfs/{id}` |
   | `prefix` | `/ipam/prefixes/{id}` |
   | `vlan` | `/ipam/vlans/{id}` |
   | `vlan_group` | `/ipam/vlans?group={id}` |
   | `scope` | `/dhcp/scopes/{id}` |
   | `pool` | `/dhcp/pools/{id}` |
   | `reservation` | `/dhcp/pools/{pool_id}?tab=reservations` |
   | `lease` | `/dhcp/leases/{id}` |
   | `template` | `/dhcp/templates/{id}` |
   | `option` | `/dhcp/options?q={option_code}` |
   | `cluster` | `/ha/clusters/{id}` |

   Reservations, VLAN groups and options have no page of their own, so they link to the
   page that hosts them, pre-filtered — which required a `?tab=` input on the pool detail
   page, `?q=` on the option catalogue and `?group=` on the VLAN list.

2. **No match returned JSON `null`, not `[]`.** The frontend iterated it and threw, which
   is the "something went wrong" an operator saw on any query that simply had no hits.
   `GlobalSearch` now returns an empty slice; both callers are also `?? []` defensive.

3. **Coverage was partial.** `SearchResultType` gains `vlan`, `vlan_group` and `option`.
   The IP/CIDR branch and the substring branch now run *together* rather than the
   structural one excluding the text one, so `10.10.1.65` finds the lease on that address
   *and* anything whose name or description mentions it. Substring matching reaches the
   text form of every address column (`host()`/`text()`), reservation binding keys,
   hex-encoded lease client IDs, VLAN IDs, option codes, and `entity_tags` for prefixes
   and VLANs as well as the kinds that already had it. LIKE wildcards in the query are
   escaped, so searching for `%` no longer matches everything.

Results are ranked exact-title, then title-prefix, then entity kind, and **each kind is
capped at `max(5, limit/5)` rows**. Without the cap a partial address like `10.10.1` fills
the entire limit with leases and the prefix, scope and pool containing that address never
appear — which reads as "search can't find my scope". Global search is a jump-to
navigator; the per-entity list pages are where an exhaustive match belongs.

The 'user' and 'server' exclusion from A15's admin-only gate stands unchanged.

### A18.3 Navigation
- `/dhcp` gains a **Leases** tab: `/dhcp/leases` (every lease, searchable, with bulk
  selection and the A18.1 actions) and `/dhcp/leases/{id}`. Leases were previously
  reachable only by drilling scope → pool → Leases tab, which left global search with
  nowhere to send a lease hit.
- `dhcp/leases/leases-table.component.ts` is shared: the pool detail page's Leases tab is
  that component with `[poolId]` set, so the two never drift apart.
- A17.7's "pool Leases tab → Reserve" action is now labelled **Pin** and is available in
  bulk. Pin creates a reservation from the client currently holding the address; a bulk
  pin skips leases that identify no client and reports the tally.
- The `gdhcp` wordmark moves from the toolbar to the top of the sidenav, above the nav it
  belongs to, and links to `/dashboard`. The global search trigger keeps the toolbar's
  leading position.

## A19. Reservation visibility and the pin toggle
Added after A18. The pin was write-only: the leases table offered a Pin action but no way to see
whether an address was already pinned, so discovery required clicking and reading a Postgres constraint
error. Six defects in the reservation system are fixed here, and visuals are made consistent.

### A19.1 The write-only pin
`UNIQUE (pool_id, binding_key)` in the schema is correct — one reservation per client per pool. The
defect was that nothing in the read path exposed it. When an operator pinned a client that was already
pinned to a different address, the API returned `duplicate key value violates unique constraint
"dhcp_reservations_pool_id_binding_key_key"` — a constraint name, not a sentence. The leases read
path now carries `reservation_state` and `reservation` fields, so the two web tables and the CLI
all answer the same question: is this client pinned?

### A19.2 Five reservation states
A lease's `LeaseReservationState` names its relationship to reservations in its pool. Each affects
DHCP allocation visibility and the client's lease stability differently:

| State | Meaning | Allocation | Stability |
| --- | --- | --- | --- |
| `not_pinned` | No reservation for this client or address in this pool. Pinning is safe. | Free to allocate dynamic addresses. | Client holds address only while lease is active; renew affinity brings it back. |
| `pinned` | Enabled reservation binds this exact client to this exact address. | Address is held and only offered to this client. | Client is guaranteed this address; address is unavailable to any other client. |
| `pinned_disabled` | Reservation exists but is disabled (`is_enabled = false`). `reservationForKeys` and `scanCandidateIPs` both filter `AND is_enabled`, so disabled reservations are invisible to allocation. | Address is free; no DHCP client can claim it, not even the reserved one. | Disabled reservations hold neither addresses nor block anyone. Re-enabling one makes it visible again. |
| `pinned_elsewhere` | This client is reserved, but to a **different** address. Moving the pin requires updating the existing reservation, not creating a new one. Creating a second would violate `UNIQUE (pool_id, binding_key)`. | Client receives whichever of {renew affinity, dynamic candidate} applies. Reserved address sits unclaimed. | Client keeps current address via renew affinity; migration to the reserved address requires DHCP forgetfulness or operator intervention. |
| `address_reserved` | This address is reserved for a different client. Current lease holder keeps it until lapse. | Address is unavailable to other clients. | Current holder is not disturbed; when lease lapses, reserved client can claim it. |

### A19.3 State derivation
The state is derived on read from a LEFT JOIN LATERAL over `dhcp_reservations`, ordered by specificity
(A19.2's table establishes precedence):

1. Both binding_key and reserved_ip match (client pinned to address): `pinned` if enabled, `pinned_disabled` if not.
2. Only binding_key matches (client reserved elsewhere): `pinned_elsewhere`.
3. Only reserved_ip matches (address reserved for someone else): `address_reserved`.
4. No match: `not_pinned`.

The SQL function `lease_binding_keys(client_mac, client_id, client_hostname)` returns the lease's
canonical candidate keys in `models.ClientIdentity.BindingKeys()` order — the single definition shared
by Postgres and the mock (A17.1 corrected one such inconsistency already). The LATERAL orders by
`array_position()` to preserve candidate precedence, ensuring the allocation algorithm's tie-breaking
logic is visible in reads. Only the API-facing read paths (not the DHCP engine's hot-path allocation
calls) populate `ReservationState`; the engine leaves it empty.

`models.ClassifyLeaseReservation` is the sole definition called by both Postgres and mock; the two
can never disagree about what "pinned" means.

### A19.4 The pin is a toggle: pin / unpin / move
`POST /leases/{lease_id}/pin` toggles: if the lease is not pinned, it creates a reservation; if
pinned, it deletes it. Moving a reserved client to a different address requires updating the
reservation, not adding a new one. This demanded one store signature change: `PUT /reservations/{id}`
now accepts an optional `reserved_ip` field, so a reservation's address can be moved. An absent key
now means "leave alone" — both the old and new updates (hostname/description and is_enabled) must be
able to leave some fields untouched, else an enable/disable toggle (which sends `{is_enabled}` alone)
will erase hostname and description.

### A19.5 Two write-path defects fixed
The store now rejects duplicate reservations on the same address in a pool. The state machine was
silent: two clients could have reservations for the same IP, and whichever asked second would fail to
allocate and get NAK'd. That was a starvation bug — the address would be held and unclaimed.
`ErrReservationIPTaken`, wrapped alongside `ErrConflict` for a 409 response, stops this.

A second defect: `PUT /reservations/{id}` unconditionally replaced `hostname` and `description`, so
the Reservations tab's enable/disable slide-toggle (which sends only `{is_enabled}`) erased both
fields. Now an absent key means "leave it as is", so partial updates are safe.

### A19.6 Error messages are sentences, not constraint names
`models.ErrReservationClientExists` / `ErrReservationIPTaken` / `ErrReservationIPLeased`, each
wrapped alongside `models.ErrConflict`, are mapped by the handler to clear, actionable sentences
plus a machine-readable `reason` detail. The rule: anything placed in a non-500 error string is
user-visible, because `httpx.RespondError` sends `err.Error()` straight through to JSON and the
web UI. Postgres constraint names were leaking into that channel before; now they never are.

### A19.7 Visual consistency
One pin vocabulary is now defined once in `web/src/app/shared/components/pin-indicator/` and used
in both the leases table (`leases-table.component.ts`) and the IPAM clients table
(`prefix-clients.component.ts`):

- `<app-pin-chip [state]="lease.reservation_state" [reservation]="lease.reservation" />` displays the state
  compactly (the label or icon).
- `<app-pin-button [state]="lease.reservation_state" [reservation]="lease.reservation" [disabled]="..."
  [label]="ip" (toggle)="handlePin($event)" />` is the action button.

Both are standalone; the host component imports the component class.

`.gdhcp-table`, `.gdhcp-table th`/`td`, `.gdhcp-table th.actions`/`td.actions`, `.table-wrap`, and
`.gdhcp-state-chip` (with `.state-active`, `.state-expired`, `.state-released`, `.state-abandoned`)
were copy-pasted into nine component style blocks. These are now in `web/src/styles.scss` (global);
the local copies are deleted. Global styles reach emulated-view-encapsulation components.

The CSS vocabulary for binding state uses `.state-active`, etc., uniformly across chips. The icon
for pinning changed from `bookmark`/`bookmark_add` to `push_pin` to match Material's reservation
semantics.

Layout rule: on a page hosting both the leases (children) and reservations (addresses) sections, the
leases come first — children are the structure, addresses are the detail.

### A19.8 Smaller fixes
- `ListLeases` now honours `ListParams.Search`, so the shared leases-table component's search box
  is no longer a no-op on the pool detail page.
- `idx_dhcp_reservations_pool_reserved_ip` added for fast address-side lookups.

A2's CLI verb list: the `lease` verb gains a `PINNED` column to the leases table (no new verbs).

## A20. Address status, one row per address, subnet accounting
Added after A19. Four concurrent defects infected the client and utilization surfaces, all rooted
in using the DHCP protocol binding state as an operator-visible status. This amendment replaces it with a
single status vocabulary, fixes the duplicate-row defect in the IPAM clients listing, corrects
utilization rollups for subnets, and establishes stable pagination.

### A20.1 Address status replaces binding-state rendering
`LeaseBindingState` enumerates DHCP protocol facts, not an address status: `released` and `expired`
are operationally identical to the allocator (both cause `claimLeaseIP` to reclaim the address with
one `binding_state IN ('expired', 'released')` clause), yet the UI rendered them as two distinct
coloured conditions. The utilization panel on the same page already counted both as free, but the
leases table showed two different statuses — giving operators the false impression that 'released'
and 'expired' required different handling.

The single operator-facing status vocabulary is `AddressStatus`:

| Status | Binding State(s) | Meaning |
| --- | --- | --- |
| `free` | `released`, `expired`, or no lease | Address is available; claimLeaseIP reclaims expired/released. |
| `assigned` | `active` | A client currently holds this address. |
| `reserved` | enabled reservation, no active lease | Address is held for a client not currently on it. |
| `abandoned` | `abandoned` | Held back after a conflict; claimLeaseIP refuses to reclaim it. |

`models.ClassifyAddressStatus` is the SOLE definition of this mapping, called by both Postgres and
the mock store (exactly as A19.3 established for reservation state). A DISABLED reservation (`is_enabled
= false`) deliberately does not make an address reserved — `scanCandidateIPs` filters `AND is_enabled`,
so a disabled reservation holds nothing and the address remains free.

Both `models.DHCPLease` and `models.DHCPClient` carry an `AddressStatus` field populated on read; the
CLI and web surfaces render this field in place of the raw binding state.

### A20.2 One row per address in GET /prefixes/{id}/clients
Amendment A17.6 defined the response as a UNION of leases and reservations: the same query that
brought back enabled reservations also brought back any lease for that address. When an address had
both a lease and an enabled reservation, the client returned TWO rows — which made "pin this lease"
appear to ADD a duplicate row rather than link the existing lease to a reservation. Both rows carried
the same IP but different `kind` values (`lease` vs. `reservation`).

The reservation branch is now ANTI-JOINED on address identity: `NOT EXISTS (SELECT 1 FROM dhcp_leases
WHERE pool_id = res.pool_id AND ip_address = res.reserved_ip)`. This ensures one address yields at
most one row. A lease row that has a reservation association surfaces `ReservationState` and
`Reservation` fields (amendment A19), so the single row carries both pieces of information.

This anti-join does NOT collapse a client holding one address while pinned to a different one: those
are genuinely two addresses, so two rows. A collapsed reservation row loses the reservation's own
`hostname`, `description` and `created_at` — still available on the pool's Reservations tab — but
gains `address_status` from the system status enum (A20.1), which the UI renders uniformly.

Disabled reservations are now admitted to the listing (UNION does not filter `is_enabled`), so operators
can see and re-enable them. The response structure remains unchanged: a disabled reservation carries
`is_enabled = false` and `ReservationState` set by ClassifyLeaseReservation (which handles disabled ones
— see A19.2).

The `kind` filter is redefined: it now predicts over the row rather than its provenance. `kind=lease`
selects rows where `BindingState IS NOT NULL` (came from a lease). `kind=reservation` selects rows where
`BindingKey IS NOT NULL` (came from a reservation). These predicates now OVERLAP rather than partition —
a row with both a lease and a reservation matches both filters (though the anti-join ensures no single row
comes from both branches at once).

### A20.3 Address status filtering (store layer, API layer pending)
The store implementations are ready for a `status` filter alongside the existing `state` filter,
distinguished:

- `state` is protocol-level: `active|expired|released|abandoned`. It applies only to leases (reservations
  have no binding state) and may be omitted.
- `status` is operator-level: `free|assigned|reserved|abandoned`. It applies to both leases and
  reservations, and its SQL predicates MUST stay in lockstep with `models.ClassifyAddressStatus`, the
  sole definition. A row matches `status=free` when `ClassifyAddressStatus(state, reservation)` returns
  `AddressFree`, and so on.

Postgres's WHERE clause for status filtering must replicate the same precedence `ClassifyAddressStatus`
implements (see postgres_lease.go and postgres_ipam.go WHERE clause comments). The mock store must also
classify each row identically (see mock.go). Any divergence is a correctness defect.

The API handlers (`leases/handler.go` and `ipam/clients.go`) have not yet been updated to expose the
status filter to HTTP clients; they currently parse only `state`, `kind`, and `q` parameters. Once the
handlers are updated to include `status` in their `ParseListParams` call, the `status` query parameter
will become available to API clients (web UI, CLI, etc.). The store layer is ready and waiting.

### A20.4 Subnet accounting
**THE GOVERNING RULE: a subnet is one broadcast domain — an address space of 2^(32-len) — and DHCP
pools are a subset WITHIN it.** Amendment A16.3's "two views" model is correct only for aggregates and
containers, which hold prefixes rather than hosts. A subnet page accounts over the WHOLE subnet.

The concrete defect: a /24 showed `space_total=256`, `usableHosts()=254`, and a bare "total" label of
`201` (the pool range). The 53 addresses inside the subnet but outside any pool were counted nowhere,
and narrowing a pool's range would LOWER the subnet's utilization pct — as if the subnet had shrunk rather
than the pool.

The utilization model now segregates subnet space into three non-overlapping pieces:

| Field | Value | Meaning |
| --- | --- | --- |
| `reserved_infra` | 2 (or 0 for /31,/32) | Network and broadcast addresses. |
| `pool_total` | 50 | Addresses inside DHCP pools in this subnet. |
| `static_total` | 204 | Addresses inside the subnet but outside every pool. |

**INVARIANT: `reserved_infra + static_total + pool_total == space_total`**, always. For a /24,
256 = 2 + 204 + 50. These three fields appear on subnets only; they are zero for aggregates and
containers (which contain prefixes, not addresses).

A subnet's cached `utilization_percentage` is now recomputed over the whole subnet (not just the pool
range): `(active_leases + reserved_unl leased) / pool_total * 100`. Narrowing a pool's range raises
`static_total` and lowers `pool_total`, holding `space_total` and the pct constant. A subnet's available
addresses are `pool_total - (active + reserved_unleased)`, only the addresses the DHCP pool can allocate.
The 53 static addresses are never in the available count — they are outside every pool and the DHCP engine
never touches them.

### A20.5 Utilization must not be recomputed in templates
`PrefixUtilization.Available` is `total - active - reserved_unleased` and is the authoritative
available-address count. Four screens summed `active + reserved` (ALL enabled reservations) and
computed `used = active + reserved`, then `available = total - used`. A leased reservation counted
twice (once in `active`, once in `reserved`), so `used` could exceed `total`, and templates' own
`available` could go NEGATIVE. The utilization bar masked this with `Math.min(100, ...)`, hiding a data
integrity problem.

Binding: **Available must never be recomputed in a template.** Every page must render
`PrefixUtilization.Available` as-is from the server. It has already accounted for the lease-bound
reservation correctly.

### A20.6 Pagination: stable sort order
`GET /prefixes/{id}/clients` ORDER BY had no tiebreaker — only `ip_address`, `lease_end` and
`client_hostname`. `ip_address` is not unique across scopes in a subtree, `lease_end` is NULL for
every reservation row (and identical for leases renewed as a batch), and `client_hostname` is
nullable and often duplicate. Rows could repeat or vanish across pages as the query plan shifted
at random.

A stable id tiebreaker is now required on EVERY sort: `ORDER BY <primary>, id` ensures rows stay in
order across pagination. The id is the lease id (for lease rows) or reservation id (for reservation
rows) — both guaranteed unique within the result set.

### A20.7 Frontend rendering
One status vocabulary, defined once in `web/src/app/shared/components/address-status/`, renders
AddressStatus identically everywhere the API surfaces it. The pin vocabulary from A19.7 remains
separate and unchanged.

A2's CLI: the `lease` and `prefix clients` commands' binding-state columns are replaced with
AddressStatus columns, using the same `ClassifyAddressStatus` logic as the API so all surfaces agree.


## A21. The VLAN detail page, VLAN tagging, and where VLANs live in the nav
Added after A20. A16.1 built the VLAN subsystem and A16.2 declared VLANs taggable, but the
frontend never finished either: the detail page could not render at all, and nothing in the UI
ever attached a tag to a VLAN.

### A21.1 `effect()` that loads data must set `allowSignalWrites`
`/ipam/vlans/{id}` rendered an empty box. `VlanDetailComponent` kicked its loads off from

```ts
effect(() => this.load(this.id()));
```

and `load()`'s first statement is `this.loading.set(true)`. On Angular 18 a synchronous signal
write inside an effect throws `NG0600` unless the effect opts in, so the effect died before it
issued a single request: `loading` stayed `true` forever and no data was ever fetched. The
server was never at fault — `GET /vlans/{id}` and `GET /vlans/{id}/prefixes` both answered
correctly throughout.

**Binding: every `effect()` that drives a load from a route-bound `input()` must pass
`{ allowSignalWrites: true }`,** as `PrefixDetailComponent` and the DHCP detail pages already
do. `ngOnInit` is not an alternative here: with `withComponentInputBinding()` the router reuses
the component instance when navigating between two VLANs and only updates the `id` input, so an
`ngOnInit`-only load leaves the page frozen on the previous entity.

### A21.2 `.skeleton-line` is component-local, not global
The failure above was invisible rather than loud because the loading skeleton it was stuck on
had no styles. `.gdhcp-card`, `.gdhcp-muted`, `.gdhcp-mono` and `.gdhcp-table` are global (see
`web/src/styles.scss`); `.skeleton-line` and its `shimmer` keyframes are **not** — each page
carries its own copy. A page that writes `<div class="skeleton-line">` without defining the
class renders a zero-height nothing, which is indistinguishable from a broken page. Either
define it in the component's `styles` or do not use it.

### A21.3 VLAN tagging is wired up
A16.2 added `vlan` to `TaggableEntityType` and the server's `validEntityType`, but no screen
used it. VLANs now carry tags the same way VRFs and prefixes do, through `EntityTagsService`
with the entity type in `VLAN_ENTITY_TYPE` (`ipam/data-access/entity-tags.service.ts`),
alongside `PREFIX_ENTITY_TYPE`:

- the detail page renders editable chips and an inline-editable description;
- the `/ipam/vlans` list renders a read-only Tags column.

Tags cost one request per row — there is no bulk "tags for many entities" endpoint — so the
VLANs list now **paginates** (25/50/100) instead of pulling `limit=200` in one go. Any list
that grows a tags column must bound its page size for the same reason.

### A21.4 Navigation: VLANs belongs in the sidenav
VLANs was reachable only from a `mat-stroked-button` in the IPAM overview's page header, which
put the layer-2 axis of the section behind the layer-3 one. A16.4 moved the prefix tree into
`/ipam`'s split layout, and that tree cannot represent a VLAN — a VLAN is not a node in the
prefix hierarchy — so the section's own navigation has nowhere to put it.

`NavLink` in `layout/shell.component.ts` gains an optional `children`, rendered indented beneath
its parent and always visible. IPAM's single child is **VLANs** (`/ipam/vlans`). The header
button is gone; the explainer paragraph's inline link stays, since it is prose, not navigation.

### A21.5 The shared prefix table's VRF column is opt-in
`ipam/components/prefix-table.component.ts` is shared by the VRF detail page, the prefix detail
page and now the VLAN detail page. The first two show prefixes that are all in one VRF, where a
VRF column is noise; a VLAN's prefixes are not, because the same layer-2 segment can be numbered
in more than one routing instance. The column is therefore driven by an optional
`vrfNamesById` input — supply the map to get the column, omit it to leave the table as it was.
`GET /vlans/{id}/prefixes` returns `vrf_id` but no VRF name, so the caller resolves names with
one `GET /vrfs` call.

## A22. Answering clients in INIT, the stored lease length, and template option lookup
Three defects found by running the engine against real DHCP clients on an isolated L2 segment
rather than against unit tests. Each is independent; they are grouped because one test run
surfaced all three.

### A22.1 A reply to a client that has no address yet must be delivered by hardware address
`replyDestination`'s case 4 — neither `giaddr` nor `ciaddr` set and the client left the BOOTP
broadcast flag clear — addressed the reply to `yiaddr` and wrote it to the ordinary UDP socket.
That cannot work. The client does not hold `yiaddr` yet, so the kernel ARPs for it, nothing
answers (nothing *can* answer for an address that has not been handed out), and the reply is
dropped in the queue. The client never sees an OFFER.

This is not an edge case. ISC dhclient, busybox udhcpc, Windows and macOS all leave the
broadcast flag clear by default; a packet capture of either Linux client shows
`Flags [none] (0x0000)`. In practice the engine answered only the clients that opted into
broadcast, which is close to none of them.

`server/internal/dhcp/rawsend.go` opens one `AF_PACKET` socket per interface (`packet.Datagram`,
so the kernel still builds the Ethernet header) and writes the IPv4 and UDP headers itself,
addressed to the client's `chaddr`. No routing decision, no ARP. This is what ISC dhcpd,
dnsmasq and Kea do for the same reason.

The socket needs `CAP_NET_RAW`, which `deploy/docker/Dockerfile`'s `setcap` and
`deploy/systemd/gdhcp.service`'s `AmbientCapabilities` already grant. When it cannot be opened
the engine logs a warning naming the missing capability and falls back to the old UDP write, so
a constrained runtime degrades to the previous behaviour instead of failing to start.

Only case 4 changes. A relayed reply goes to the relay, a renewing client is reachable at
`ciaddr`, and a client that did set the broadcast flag gets a broadcast — all three remain
ordinary UDP writes, since all three are addressable by IP.

### A22.2 The stored lease length is the resolved lease length
`Postgres.AllocateLease` opened with `leaseTime := 24 * time.Hour` and had no parameter to
override it. The engine resolved Option 51 through the full inheritance chain, advertised that
value in the OFFER, and then wrote a 24-hour row regardless. With
`default_lease_time: 600` the OFFER said 600s, the ACK said 86399s and the database said one
day: three different answers to one question, and the reaper expiring leases a day after the
client believed they had lapsed.

`AllocateLease` now takes a `leaseDur time.Duration`. `dhcp/allocation.go` resolves it per
scope/pool *before* claiming — the duration depends on the pool, and the pool is chosen during
allocation — so the persisted expiry and the advertised expiry come from the same call.
A non-positive `leaseDur` still falls back to 24h, so a caller that passes nothing can never
write a zero-length lease.

### A22.3 `orderedTemplateIDs`' empty-string guard was really a uuid cast
The query read `WHERE scope_id = $1 AND $1 <> ''`, intending "skip this arm when no scope was
given". Postgres infers `$1`'s type as `uuid` from the equality, then applies that type to the
`''` literal too, so every execution failed with `invalid input syntax for type uuid: ""` —
whatever the arguments were, including the ordinary case where both IDs are valid. The template
tier of option inheritance had therefore never resolved anything; each lookup logged
`resolve option failed, skipping` and fell through. A single DORA exchange produced dozens.

An absent scope or pool is now passed as a NULL uuid instead. `scope_id = NULL` is never true,
which is the skip the guard was reaching for, and the comparison the cast came from is gone.
