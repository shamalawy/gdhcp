# gdhcp

gdhcp is a high-availability DHCP server with built-in IP address management
(IPAM), shipped as a single Go binary. It runs the DHCPv4 engine, a REST
API, and an embedded Angular web UI out of one process, backed by
PostgreSQL as the single source of truth — no local state, no separate
database-per-node.

- **VRF-aware**: scopes and IPAM prefixes are scoped to a VRF, so
  overlapping/private CIDRs across routing domains coexist safely.
- **HA clusters with a floating VIP**: nodes in a cluster run a
  Postgres-backed heartbeat/election (no VRRP daemon, no keepalived
  dependency) and move a virtual IP between themselves on failover.
- **Single binary**: `gdhcp` is a DHCP server, a REST API server, an
  embedded UI, and the CLI client for that API, all in one executable.

## What's actually in the box

| Area | What it does |
|---|---|
| DHCPv4 engine | DISCOVER/OFFER/REQUEST/ACK, relay agent (option 82) support, per-VRF interface binding, atomic lease allocation safe under concurrent server load, reservation matching by seven client identifier types (MAC, client-ID, hostname, circuit-ID, remote-ID, vendor class, user class) with deterministic precedence (see `docs/AMENDMENTS.md` A6, A17) |
| IPAM | Hierarchical prefix tree (aggregate → container → subnet), utilization tracking, tag/label support, global search, DHCP client rollup per-prefix showing leases and reservations from the prefix and all descendants |
| Option system | Full IANA DHCPv4 option catalog (codes 1–254), with inheritance across global → template → scope → pool → reservation |
| HA clustering | Multi-node clusters, heartbeat + election stored in Postgres, VIP failover via netlink + gratuitous ARP, manual failover from the CLI/API |
| REST API | `snake_case` JSON, JWT session auth + long-lived API tokens, RBAC (`admin`/`operator`/`viewer`), SSE endpoint for live dashboard/lease/cluster updates |
| Web UI | Angular 18, standalone components, signals — dashboard, IPAM, DHCP (scopes/pools/reservations/templates), HA, and admin (users/tokens) areas |
| CLI | The same binary, pointed at a running server over HTTP — `gdhcp vrf`, `gdhcp prefix`, `gdhcp scope`, `gdhcp pool`, `gdhcp lease`, `gdhcp cluster`, `gdhcp ha`, `gdhcp user`, `gdhcp search`, ... (reservations are under `pool reservation`; clients are under `prefix clients`) |
| Metrics | Prometheus endpoint, separate port from the API |

See `docs/ARCHITECTURE.md` for the full design and `docs/AMENDMENTS.md` for
the binding corrections to it (that file wins over every other doc,
including this README, wherever they disagree).

## Quickstart

Pick one of three deployment paths. All three end with the same first
login: **http://\<host\>:8080, username `admin`, password `admin`** — see
[First login](#first-login) below for what that credential means and how
to mitigate it (there is currently no in-product way to change it).

### Docker Compose

```bash
cp deploy/docker/config.yaml.example deploy/docker/config.yaml
# edit deploy/docker/config.yaml — review dhcp.interfaces (starts empty,
# i.e. API/UI-only) and server.jwt_secret

docker compose -f deploy/docker/docker-compose.yml up -d --build
docker compose -f deploy/docker/docker-compose.yml logs -f gdhcp_server
curl http://localhost:8080/api/v1/health
```

The stack runs on `network_mode: host` (real DHCP traffic is broadcast and
does not cross a Docker bridge) and needs `CAP_NET_ADMIN`/`CAP_NET_RAW` for
VIP/ARP handling — both are already wired up in
`deploy/docker/docker-compose.yml`. Full detail, including two-node HA in
Docker: [docs/deploy/docker.md](docs/deploy/docker.md).

### Bare metal (systemd)

```bash
make build                       # -> dist/gdhcp (needs Go 1.26 + Node 20)

sudo ./scripts/bootstrap-db.sh   # creates the gdhcp Postgres role + database
sudo ./scripts/install.sh        # installs the binary, config, systemd unit

sudo gdhcp migrate --config-file /etc/gdhcp/config.yaml
sudo systemctl start gdhcp
systemctl status gdhcp
journalctl -u gdhcp -f
```

`install.sh` also accepts `GDHCP_RELEASE_URL=<tarball>` instead of a local
build. Full detail, including the two-node VIP walkthrough:
[docs/deploy/bare-metal.md](docs/deploy/bare-metal.md).

### Kubernetes (Kustomize)

```bash
# quick local smoke test against base's placeholder secrets — do not use
# for anything real:
kubectl apply -k deploy/kubernetes/base

# real environments: generate the overlay's secrets first, then
kubectl apply -k deploy/kubernetes/overlays/production   # or overlays/staging

kubectl get pods -n gdhcp -o wide
kubectl port-forward -n gdhcp svc/gdhcp-api 8080:8080
```

gdhcp runs as a DaemonSet with `hostNetwork: true` (same broadcast
constraint as Docker) on nodes labeled `gdhcp.io/dhcp-node=true`. VIP
failover is not native to Kubernetes — see the caveats and options in
[docs/deploy/kubernetes.md](docs/deploy/kubernetes.md).

## Build from source

Prerequisites: **Go 1.26**, **Node 20**.

```bash
make build       # builds the Angular UI (web/dist/) and dist/gdhcp — full release build
make go-build    # Go binary only; embeds whatever is currently in web/dist/
                  # (a placeholder index.html is committed there so `go build`
                  # works standalone, but run `make web` first for a real UI)
make web         # Angular build only
make test        # go test ./...
make vet         # go vet ./...
make run-dev     # go run ./server/cmd/gdhcp serve, using ./config.yaml if
                  # present (cp config.yaml.example config.yaml first),
                  # otherwise gdhcp's built-in defaults (same values as
                  # config.yaml.example)
```

`make build` is pure Go (`CGO_ENABLED=0`) — the resulting `dist/gdhcp` is a
static binary with no C dependencies (see `docs/AMENDMENTS.md` A3).

## First login

Migration `0003_seed_admin.sql` seeds a default account so there's
something to log in with on a fresh database:

- **username**: `admin`
- **password**: `admin`

This is a well-known credential exposed on your network the moment
`dhcp.interfaces` or `server.api_addr` is reachable from anywhere but
localhost. **As of this build there is no supported way to change it**:
neither `gdhcp user update` nor the web UI's edit-user dialog touch the
password field — by design, per the UI's own comment that there is no
password-reset endpoint in the API. Until one ships, treat `admin`/`admin`
as a standing credential and mitigate at the network layer (firewall
`server.api_addr` to trusted hosts, put it behind a reverse proxy/VPN, etc.)
rather than assuming it's been rotated.

```bash
gdhcp login --server http://localhost:8080 --username admin
```

## CLI tour

`dist/gdhcp` (or `gdhcp` once installed) is both the server and the API
client. Run `gdhcp --help` for the full, current command list; a few
starting points:

```bash
gdhcp serve --config-file config.yaml    # run the server (DHCP + API + UI)
gdhcp migrate --config-file config.yaml  # apply pending DB migrations and exit
gdhcp version                            # print the build version

gdhcp login --server http://localhost:8080   # authenticate, save a session
                                               # token to ~/.gdhcp.yaml

gdhcp vrf list
gdhcp scope create ...
gdhcp pool list --scope-id <id>
gdhcp lease list --scope-id <id>
gdhcp ha status                          # this node's health/HA role
gdhcp cluster failover <cluster-id> --target-server-id <server-id>
gdhcp search "10.0.0"                    # search across VRFs, scopes,
                                          # pools, reservations, leases, ...
```

Every subcommand accepts `--server`, `--token`, and `--output
table|json|yaml`; `gdhcp login` persists `--server`/the session token to
`~/.gdhcp.yaml` so you don't need to repeat them.

Note the two distinct `--config*` flags: `--config` (global) is the CLI
**client's** config file (server address + saved token, written by `gdhcp
login`); `--config-file`, on `serve` and `migrate` only, is the **server's**
`config.yaml`. They are unrelated to each other.

## Configuration (`config.yaml`)

Copy `config.yaml.example` to `config.yaml` and edit, or override any field
with a `GDHCP_<SECTION>_<FIELD>` environment variable (handy for containers
and Kubernetes, where secrets shouldn't live in a checked-in file — env
overrides win over the file).

| Key | Env var | Default | Meaning |
|---|---|---|---|
| `server.api_addr` | `GDHCP_SERVER_API_ADDR` | `0.0.0.0:8080` | REST API + UI listen address |
| `server.api_tls_enabled` | `GDHCP_SERVER_API_TLS_ENABLED` | `false` | Serve the API over TLS |
| `server.api_tls_cert` / `api_tls_key` | `GDHCP_SERVER_API_TLS_CERT` / `_KEY` | — | Cert/key paths when TLS is enabled |
| `server.jwt_secret` | `GDHCP_SERVER_JWT_SECRET` | *(random at startup)* | Signs session JWTs. Empty is fine for one node (dev) but resets sessions on every restart; set an explicit shared value for any multi-node deployment |
| `database.dsn` | `GDHCP_DATABASE_DSN` | `postgres://gdhcp:gdhcp@localhost:5432/gdhcp?sslmode=disable` | PostgreSQL connection string |
| `database.max_open_conns` | `GDHCP_DATABASE_MAX_OPEN_CONNS` | `25` | Pool size |
| `database.max_idle_conns` | `GDHCP_DATABASE_MAX_IDLE_CONNS` | `5` | Idle connections kept open |
| `database.conn_max_lifetime` | `GDHCP_DATABASE_CONN_MAX_LIFETIME` | `5m` | Max connection lifetime |
| `database.lease_retention_days` | `GDHCP_DATABASE_LEASE_RETENTION_DAYS` | `90` | How long expired leases are kept before cleanup |
| `database.option_cache_ttl` | `GDHCP_DATABASE_OPTION_CACHE_TTL` | `60s` | In-memory option-catalog cache TTL |
| `dhcp.interfaces` | `GDHCP_DHCP_INTERFACES` | *(empty)* | Interfaces to listen on, each bound to a VRF. Empty = API/UI-only mode (no DHCP listeners) — used for IPAM-only nodes and local dev. Env form: `eth0:default,eth1:mgmt` |
| `dhcp.default_lease_time` | `GDHCP_DHCP_DEFAULT_LEASE_TIME` | `86400` (24h, seconds) | Default lease length |
| `dhcp.default_renewal_time` | `GDHCP_DHCP_DEFAULT_RENEWAL_TIME` | `43200` (T1, seconds) | Default renewal time |
| `dhcp.default_rebind_time` | `GDHCP_DHCP_DEFAULT_REBIND_TIME` | `75600` (T2, seconds) | Default rebind time |
| `ha.enabled` | `GDHCP_HA_ENABLED` | `false` | `false` = standalone (still registers in the DB); `true` = contend elections |
| `ha.cluster_id` | `GDHCP_HA_CLUSTER_ID` | — | Cluster this node belongs to |
| `ha.server_id` | `GDHCP_HA_SERVER_ID` | — | This node's unique server ID |
| `ha.server_name` | `GDHCP_HA_SERVER_NAME` | — | Human-readable node name |
| `ha.node_id` | `GDHCP_HA_NODE_ID` | — | Election tie-breaker; lower `node_id` wins, unique per cluster |
| `ha.vip_address` | `GDHCP_HA_VIP_ADDRESS` | — | Floating VIP, CIDR form (e.g. `10.0.0.10/24`) |
| `ha.vip_interface` | `GDHCP_HA_VIP_INTERFACE` | — | Interface the VIP is assigned to |
| `ha.heartbeat_interval_ms` | `GDHCP_HA_HEARTBEAT_INTERVAL_MS` | `1000` | How often this node writes a heartbeat |
| `ha.heartbeat_ttl_ms` | `GDHCP_HA_HEARTBEAT_TTL_MS` | `3000` | Heartbeat staleness threshold (must be ≥ 2× interval) |
| `ha.election_interval_ms` | `GDHCP_HA_ELECTION_INTERVAL_MS` | `2000` | How often an election is (re-)evaluated (should be ≤ ttl/2) |
| `ha.config_check_interval_ms` | `GDHCP_HA_CONFIG_CHECK_INTERVAL_MS` | `5000` | How often cluster config is re-read from the DB |
| `ha.config_listen_enabled` | `GDHCP_HA_CONFIG_LISTEN_ENABLED` | `true` | Watch the DB for live cluster config changes |
| `logging.level` | `GDHCP_LOGGING_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `logging.format` | `GDHCP_LOGGING_FORMAT` | `json` | `json` or `text` |
| `logging.output` | `GDHCP_LOGGING_OUTPUT` | `stdout` | Log destination |
| `metrics.enabled` | `GDHCP_METRICS_ENABLED` | `true` | Enable the Prometheus endpoint |
| `metrics.listen_addr` | `GDHCP_METRICS_LISTEN_ADDR` | `0.0.0.0:9090` | Metrics listen address (separate port from the API, deliberately unauthenticated) |
| `metrics.path` | `GDHCP_METRICS_PATH` | `/metrics` | Metrics path |

Full schema: `server/internal/config/config.go`; every field and env var:
`config.yaml.example` and `server/internal/config/loader.go`.

## HA topology example: 2-node cluster with a floating VIP

Two nodes sharing one PostgreSQL database and a VIP that DHCP clients (or a
relay agent's `giaddr`) point at instead of either node's real address —
failover moves the VIP and is invisible to clients. Both nodes must sit on
the same L2 segment as the VIP (failover uses gratuitous ARP, which doesn't
cross a routed boundary) and reach the same `database.dsn`.

**node1** — `/etc/gdhcp/config.yaml`:

```yaml
server:
  api_addr: "0.0.0.0:8080"
  jwt_secret: "<same random 32+ byte value on both nodes>"

database:
  dsn: "postgres://gdhcp:changeme@10.0.0.5:5432/gdhcp?sslmode=disable"

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

**node2** — identical, except:

```yaml
ha:
  server_id: "dhcp-02"
  server_name: "node2"
  node_id: 2
```

`jwt_secret` must be the **same value** on both nodes, or a session created
against whichever node currently holds the VIP gets rejected if a request
lands on the other one. Full walkthrough, troubleshooting, and the
Docker/Kubernetes equivalents: [docs/deploy/bare-metal.md](docs/deploy/bare-metal.md#ha-pair-walkthrough-two-nodes--vip).

## API

Full endpoint reference, including request/response shapes and status
codes: [docs/api/endpoints.md](docs/api/endpoints.md). Base URL `/api/v1`,
`Authorization: Bearer <token>` (session JWT from `/auth/login`, or a
long-lived API token from `gdhcp user token create`), all JSON in
`snake_case`.

## Metrics

Prometheus metrics are exposed on `metrics.listen_addr` (default
`0.0.0.0:9090`) at `metrics.path` (default `/metrics`) — a separate port
from the API, deliberately unauthenticated for scraping. Covers DHCP
transaction counters, lease allocation contention, HA heartbeat/election
latency, VIP assignment failures, and standard Go/process/DB-pool metrics.
See [docs/operations/monitoring.md](docs/operations/monitoring.md) for the
full metric list and example alert rules.

## Documentation

See [docs/README.md](docs/README.md) for the full documentation index.
`docs/AMENDMENTS.md` is binding — where any other doc (including this
README) disagrees with it, it wins.

## License

MIT — see [LICENSE](LICENSE).
