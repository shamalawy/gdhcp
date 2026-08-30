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
