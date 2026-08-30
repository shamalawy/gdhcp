# gdhcp Repository File Tree

**Module Path**: `github.com/shamalawy/gdhcp`  
**Build Target**: Go 1.26+ (CGO required for PostgreSQL driver)  
**Binary Output**: Single binary `gdhcp` with embedded frontend and migrations

---

## Root-Level Files

```
/home/shereif/code/gdhcp/
├── go.mod                          # Go module definition
├── go.sum                           # Go dependencies lock file
├── Makefile                         # Build targets: all, web, build, test, clean, lint
├── .gitignore
├── .github/
│   └── workflows/
│       ├── ci.yaml                  # GitHub Actions CI/CD
│       └── release.yaml              # Release build workflow
├── README.md                        # Project overview
├── LICENSE                          # License file
└── config.yaml.example              # Example configuration file
```

**Ownership**: Build infrastructure and CI/CD templates (no single owner; shared)

---

## Documentation (`docs/`)

```
docs/
├── README.md                        # Documentation index
├── ARCHITECTURE.md                  # Architecture contract (final, binding)
├── FILETREE.md                      # This file
├── CONTRACTS.md                     # Go interfaces and API contracts
├── design/                          # Design documents (read-only reference)
│   ├── data-model.md                # Database schema design rationale
│   ├── dhcp-engine.md               # DHCP packet handling design
│   ├── ha-cluster.md                # HA clustering and VIP failover design
│   ├── api-cli.md                   # API and CLI design
│   ├── deploy-build.md              # Deployment and build guide
│   ├── frontend.md                  # Frontend/Angular design
│   └── index.md                     # Design doc index
├── db/
│   └── schema.sql                   # Complete database DDL (migration 0001)
├── api/
│   ├── endpoints.md                 # Complete REST API endpoint reference table
│   └── errors.md                    # Error code catalog
├── deploy/                          # Deployment guides
│   ├── bare-metal.md                # Systemd installation guide
│   ├── docker.md                    # Docker Compose guide
│   ├── kubernetes.md                # Kubernetes deployment guide
│   └── troubleshooting.md           # Operational runbooks
└── operations/
    ├── monitoring.md                # Prometheus metrics and alerting guide
    ├── backup-recovery.md           # PostgreSQL backup procedures
    └── performance-tuning.md        # Capacity planning and tuning
```

**Ownership**: Documentation authors (peer agents); stable reference material

---

## Go Backend (`server/`, ownership by unit)

### Entry Points

```
server/cmd/
├── gdhcp/
│   └── main.go                      # Server binary entry point
│       - Embeds migrations, web assets
│       - Sets up logging, config loading
│       - Starts HTTP API and DHCP engines
│       [Owned by: Foundation Unit]
└── gdhcp-cli/
    └── main.go                      # CLI binary entry point
        - Cobra command setup
        - Routes to CLI subcommands
        [Owned by: CLI Unit]
```

### Core Application (`internal/`)

```
server/internal/
├── models/
│   ├── user.go                      # User, APIToken model
│   ├── cluster.go                   # Cluster, Server, Heartbeat models
│   ├── vrf.go                       # VRF model
│   ├── ipam.go                      # IPAMPrefix model
│   ├── dhcp.go                      # DHCPScope, Pool, Reservation, Lease models
│   ├── options.go                   # DHCPOption, DHCPOptionValue models
│   ├── template.go                  # DHCPTemplate model
│   ├── tag.go                       # Tag, EntityTag models
│   ├── stats.go                     # Utilization/transaction stats models
│   └── errors.go                    # Domain errors and error helpers
│   [Owned by: Foundation Unit - Shared Models]

├── config/
│   ├── config.go                    # Config struct definitions
│   ├── loader.go                    # YAML/env config loading
│   ├── validator.go                 # Config validation (heartbeat constraints, etc.)
│   └── example.yaml                 # Default config example
│   [Owned by: Foundation Unit - Configuration]

├── db/
│   ├── migrate.go                   # Migration runner (embedded SQL files)
│   ├── postgres.go                  # PostgreSQL connection pooling
│   ├── conn.go                      # Database connection management
│   ├── tx.go                        # Transaction utilities
│   ├── migrations/                  # Embedded migration SQL files
│   │   ├── 0001_initial_schema.sql  # Complete schema DDL
│   │   ├── 0002_seed_dhcp_options.sql # IANA option catalog seeding
│   │   └── ...                      # Future migrations
│   └── healthcheck.go               # DB healthcheck
│   [Owned by: Foundation Unit - Database Access]

├── store/
│   ├── interface.go                 # Store interfaces (LeaseStore, ConfigStore, etc.)
│   ├── postgres.go                  # PostgreSQL implementation
│   │   ├── lease_store.go           # Lease allocation/renewal/expiry
│   │   ├── config_store.go          # Scope/pool/option queries
│   │   ├── ipam_store.go            # Prefix hierarchy queries
│   │   ├── user_store.go            # User/token management
│   │   └── stats_store.go           # Utilization/transaction stats
│   ├── cache.go                     # Option caching layer
│   └── mock.go                      # Mock store for testing
│   [Owned by: Store Unit - Repository Pattern]

├── dhcp/
│   ├── engine.go                    # Main DHCP engine startup/shutdown
│   ├── listener.go                  # UDP listener per interface
│   ├── packet.go                    # DHCPPacket struct and helpers
│   ├── dora.go                      # DISCOVER/OFFER/REQUEST/ACK state machine
│   ├── release_decline.go           # RELEASE/DECLINE handling
│   ├── allocation.go                # Lease allocation algorithm (retry loop, serialization)
│   ├── options.go                   # Option inheritance resolution
│   ├── lease.go                     # Lease lifecycle, T1/T2 timers
│   ├── relay.go                     # Relay agent support (Option 82)
│   ├── vrf.go                       # VRF selection logic
│   ├── stats.go                     # Transaction statistics recording
│   ├── errors.go                    # DHCP-specific errors
│   └── logging.go                   # Structured transaction logging
│   [Owned by: DHCP Engine Unit]

├── ha/
│   ├── cluster.go                   # Cluster manager, election loop
│   ├── election.go                  # Election query and result caching
│   ├── heartbeat.go                 # Heartbeat write/read logic
│   ├── vip.go                       # VIP assignment via netlink
│   ├── arp.go                       # Gratuitous ARP sending
│   ├── config_watcher.go            # LISTEN/NOTIFY and config propagation
│   ├── state_machine.go             # Role state transitions (PRIMARY/SECONDARY/ERROR)
│   ├── failover.go                  # Failover coordination
│   └── metrics.go                   # HA-specific Prometheus metrics
│   [Owned by: HA Cluster Unit]

├── api/
│   ├── router.go                    # HTTP route definitions (gorilla/mux)
│   ├── middleware.go                # Auth, CORS, error handling middleware
│   ├── handler.go                   # Generic handler wrapper (JSON marshalling, error formatting)
│   ├── auth/
│   │   ├── handler.go               # POST /api/v1/auth/login, /auth/logout
│   │   └── jwt.go                   # JWT token generation/validation
│   ├── users/
│   │   └── handler.go               # CRUD /api/v1/users
│   ├── clusters/
│   │   ├── handler.go               # CRUD /api/v1/clusters
│   │   └── health.go                # GET /api/v1/cluster/health
│   ├── vrfs/
│   │   └── handler.go               # CRUD /api/v1/vrfs
│   ├── ipam/
│   │   ├── handler.go               # CRUD /api/v1/prefixes
│   │   ├── tree.go                  # GET /api/v1/ipam/tree (prefix hierarchy)
│   │   └── search.go                # GET /api/v1/search (global search)
│   ├── scopes/
│   │   └── handler.go               # CRUD /api/v1/scopes
│   ├── pools/
│   │   └── handler.go               # CRUD /api/v1/pools
│   ├── reservations/
│   │   └── handler.go               # CRUD /api/v1/reservations
│   ├── leases/
│   │   ├── handler.go               # GET /api/v1/leases (listing/lookup)
│   │   └── actions.go               # release/abandon/renew/delete + /leases/bulk
│   ├── options/
│   │   └── handler.go               # CRUD /api/v1/options, GET /api/v1/options/catalog
│   ├── templates/
│   │   └── handler.go               # CRUD /api/v1/templates
│   ├── tags/
│   │   └── handler.go               # CRUD /api/v1/tags
│   ├── dashboard/
│   │   └── handler.go               # GET /api/v1/dashboard/stats, /dashboard/health
│   ├── audit/
│   │   └── handler.go               # GET /api/v1/audit/log (read-only)
│   └── errors.go                    # API error definitions and formatters
│   [Owned by: API Unit]

├── cli/
│   ├── root.go                      # Root cobra command
│   ├── serve.go                     # gdhcp serve (main server command)
│   ├── migrate.go                   # gdhcp migrate (run migrations)
│   ├── dhcp/
│   │   ├── discover.go              # gdhcp dhcp discover (send DHCP DISCOVER)
│   │   ├── leases.go                # gdhcp dhcp leases (list active leases)
│   │   ├── scopes.go                # gdhcp dhcp scopes (list scopes)
│   │   └── health-check.go          # gdhcp dhcp health-check (verify socket)
│   ├── ha/
│   │   ├── status.go                # gdhcp ha status (cluster status)
│   │   ├── failover.go              # gdhcp ha failover (manual failover)
│   │   └── bootstrap.go             # gdhcp ha bootstrap (pre-seed cluster)
│   ├── ipam/
│   │   ├── prefixes.go              # gdhcp ipam prefixes (CRUD)
│   │   └── tree.go                  # gdhcp ipam tree (print hierarchy)
│   ├── user/
│   │   ├── create.go                # gdhcp user create
│   │   └── token.go                 # gdhcp user token (manage API tokens)
│   ├── config/
│   │   ├── get.go                   # gdhcp config get (show running config)
│   │   └── set.go                   # gdhcp config set (update config)
│   └── common/
│       ├── flags.go                 # Shared CLI flags (--db-dsn, --cluster-id, etc.)
│       └── formatters.go            # Output formatters (JSON, table, YAML)
│   [Owned by: CLI Unit]

├── server/
│   ├── app.go                       # Application coordinator
│   │   - Orchestrates all subsystems (dhcp, api, ha, db)
│   │   - Signal handling (SIGTERM)
│   │   - Graceful shutdown
│   ├── startup.go                   # Initialization sequence
│   ├── options.go                   # Server options (functional parameters)
│   └── logging.go                   # Logging configuration
│   [Owned by: Foundation Unit - Application]

├── metrics/
│   ├── registry.go                  # Prometheus metric registration
│   ├── dhcp.go                      # DHCP metrics (DISCOVER, OFFER, etc.)
│   ├── ha.go                        # HA metrics (heartbeat, election, VIP)
│   ├── db.go                        # Database metrics (connection pool, query duration)
│   ├── api.go                       # API metrics (request count, latency)
│   ├── lease.go                     # Lease allocation metrics (retries, conflicts)
│   └── server.go                    # Server health/uptime metrics
│   [Owned by: Foundation Unit - Observability]

├── logger/
│   ├── log.go                       # Structured logging setup (logrus)
│   ├── middleware.go                # HTTP request logging middleware
│   └── tracing.go                   # Optional OpenTelemetry setup
│   [Owned by: Foundation Unit - Logging]

├── version/
│   └── version.go                   # Version info (set via ldflags)
│   [Owned by: Foundation Unit]

└── test/
    ├── fixtures/                    # Test data
    └── helpers.go                   # Test utility functions
    [Owned by: Testing support (all units)]
```

---

## Frontend (`web/`)

```
web/
├── package.json                     # Node.js dependencies (Angular, TypeScript, etc.)
├── package-lock.json                # npm lock file
├── tsconfig.json                    # TypeScript configuration
├── angular.json                     # Angular CLI configuration
├── src/
│   ├── main.ts                      # Angular bootstrap entry point
│   ├── styles.css                   # Global styles
│   ├── index.html                   # Base HTML template
│   ├── app/
│   │   ├── app.module.ts            # Root Angular module
│   │   ├── app-routing.module.ts    # Main routing module
│   │   ├── app.component.ts         # Root component
│   │   ├── shared/
│   │   │   ├── models/              # TypeScript interfaces mirroring Go models
│   │   │   ├── services/            # HTTP API clients (auto-generated or hand-written)
│   │   │   │   ├── api.service.ts   # Central REST client
│   │   │   │   ├── auth.service.ts  # JWT/session management
│   │   │   │   └── websocket.service.ts # SSE for live updates (optional)
│   │   │   └── components/          # Reusable UI components (header, footer, etc.)
│   │   ├── dashboard/               # Dashboard feature module
│   │   │   ├── dashboard.module.ts
│   │   │   ├── dashboard.component.ts
│   │   │   ├── utilization-chart/   # Utilization gauge/chart component
│   │   │   ├── status-widget/       # Cluster status widget
│   │   │   └── lease-table/         # Active leases table
│   │   ├── ipam/                    # IPAM tree navigator feature module
│   │   │   ├── ipam.module.ts
│   │   │   ├── tree-view/           # Prefix hierarchy tree component
│   │   │   ├── prefix-detail/       # Prefix detail view
│   │   │   └── subnet-list/         # Subnet list (children of prefix)
│   │   ├── dhcp/                    # DHCP configuration feature module
│   │   │   ├── dhcp.module.ts
│   │   │   ├── scopes/              # Scope list and detail
│   │   │   ├── pools/               # Pool management (child of scope)
│   │   │   ├── reservations/        # Reservation editor (child of pool)
│   │   │   ├── options/             # Option value editor
│   │   │   └── templates/           # Template management
│   │   ├── ha/                      # HA cluster management feature module
│   │   │   ├── ha.module.ts
│   │   │   ├── cluster-list/        # Cluster list
│   │   │   ├── cluster-detail/      # Cluster status, failover, heartbeat
│   │   │   └── server-status/       # Per-server heartbeat and role
│   │   ├── admin/                   # Admin features (users, audit log)
│   │   │   ├── admin.module.ts
│   │   │   ├── users/               # User list and edit
│   │   │   ├── audit-log/           # Audit log viewer
│   │   │   └── settings/            # Global settings/config
│   │   ├── auth/                    # Authentication feature module
│   │   │   ├── auth.module.ts
│   │   │   ├── login/               # Login form
│   │   │   └── profile/             # User profile
│   │   └── layout/
│   │       ├── header/              # Navigation header
│   │       ├── sidebar/             # Left sidebar navigation (tree-based)
│   │       └── main-layout/         # Main layout container
│   └── assets/                      # Static assets (icons, images)
│       ├── images/
│       └── icons/
├── e2e/                             # End-to-end tests
└── dist/                            # Built output (generated by build process)
    [Owned by: Frontend Unit]
```

---

## Deployment & Scripts (`deploy/`, `scripts/`)

```
deploy/
├── docker/
│   ├── Dockerfile                   # Multi-stage build
│   ├── docker-compose.yml           # Local dev stack (gdhcp + postgres)
│   └── .dockerignore
│   [Owned by: Deployment Unit]

├── systemd/
│   ├── gdhcp.service                # Service unit file
│   ├── gdhcp.socket                 # (Optional) socket activation
│   └── gdhcp.env                    # Environment variables (sourced by service)
│   [Owned by: Deployment Unit]

└── kubernetes/
    ├── kustomization.yaml           # Kustomize base
    ├── base/
    │   ├── namespace.yaml           # K8s namespace
    │   ├── postgres/
    │   │   ├── configmap.yaml       # Postgres init SQL
    │   │   ├── statefulset.yaml     # Postgres StatefulSet
    │   │   └── service.yaml         # Postgres headless service
    │   └── gdhcp/
    │       ├── configmap.yaml       # gdhcp config
    │       ├── secret.yaml          # DB credentials, TLS certs
    │       ├── deployment.yaml      # gdhcp Deployment (2–3 replicas)
    │       ├── daemonset.yaml       # (Alternative) DaemonSet for per-node HA
    │       ├── service.yaml         # K8s Service (ClusterIP + LoadBalancer)
    │       └── networkpolicy.yaml   # Network policies (optional)
    └── overlays/                    # Environment-specific overrides
        ├── production/
        │   └── kustomization.yaml   # Production tuning
        └── staging/
            └── kustomization.yaml   # Staging config
        [Owned by: Deployment Unit]

scripts/
├── install.sh                       # Bare-metal installation script
│   - Downloads binary
│   - Creates systemd unit
│   - Sets Linux capabilities (CAP_NET_ADMIN for HA)
│   - Starts service
├── uninstall.sh                     # Bare-metal uninstaller
├── bootstrap-db.sh                  # Initialize PostgreSQL database
├── backup.sh                        # Database backup script
│   [Owned by: Deployment Unit]

└── build/
    ├── build.sh                     # Build script (alternative to Makefile)
    └── release.sh                   # Release build automation
    [Owned by: Build infrastructure]
```

---

## Implementation Unit Ownership Matrix

| Unit Name | Paths | Purpose |
|-----------|-------|---------|
| **Foundation** | `server/internal/{config,models,db,server,version}`, `server/cmd/gdhcp`, `docs/ARCHITECTURE.md`, `go.mod` | Core infrastructure: config loading, models, DB connection, app orchestration, entry point |
| **Store** | `server/internal/store` | Repository pattern: lease/config/IPAM data access layer, caching |
| **DHCP Engine** | `server/internal/dhcp` | DHCP packet handling, DORA, relay agent, lease allocation, option resolution |
| **HA Cluster** | `server/internal/ha` | VIP failover, heartbeat, election, state machine, failover coordination |
| **API** | `server/internal/api` | REST HTTP API handlers, routing, auth middleware, error formatting |
| **CLI** | `server/internal/cli`, `server/cmd/gdhcp-cli` | Command-line interface, cobra subcommands |
| **Metrics & Logging** | `server/internal/{metrics,logger}` | Prometheus instrumentation, structured logging, OpenTelemetry (optional) |
| **Frontend** | `web/` | Angular SPA: dashboard, IPAM tree, DHCP config, HA status pages |
| **Deployment** | `deploy/`, `scripts/` | Docker, systemd, Kubernetes manifests and deployment automation |
| **Documentation** | `docs/` | Design docs, API reference, deployment guides, operational runbooks |

---

## Embedded Assets & Migrations

### Web Assets (embedded via go:embed)

```go
//go:embed ../../../web/dist/*
var WebAssets embed.FS
```

**File**: `server/cmd/gdhcp/main.go`  
**Includes**: All built Angular app files (HTML, JS, CSS, assets)  
**Runtime**: Served by HTTP handler at `/` and API routes at `/api/v1/*`

### Database Migrations (embedded via go:embed)

```go
//go:embed ../../migrations/*.sql
var Migrations embed.FS
```

**File**: `server/cmd/gdhcp/main.go`  
**Location**: `server/migrations/`  
**Naming**: `0001_initial_schema.sql`, `0002_seed_dhcp_options.sql`, etc.  
**Execution**: On startup via `internal/db/migrate.go`; runs all pending migrations in transaction order.

---

## Build Outputs

**Makefile targets** produce:

- `dist/gdhcp` — Main server binary (includes frontend + migrations)
- `dist/gdhcp-cli` — CLI binary (mirrors API, no frontend)
- `dist/migrations/` — SQL migration files (for reference; embedded in binary)
- `dist/config.yaml.example` — Example configuration

**Docker image**: `gdhcp:latest` from `deploy/docker/Dockerfile`  
**Kubernetes manifests**: in `deploy/kubernetes/` (kustomizable)

---

## Development Workflows

### Adding a New API Endpoint

1. **Define handler** in `server/internal/api/{resource}/handler.go`
2. **Add route** in `server/internal/api/router.go`
3. **Update frontend** model/service in `web/src/app/shared/`
4. **Write tests** in `{unit}_test.go` file
5. **Update API doc** in `docs/api/endpoints.md`

### Adding a Database Table

1. **Create migration** file: `server/migrations/000X_name.sql`
2. **Define Go model** in `server/internal/models/{entity}.go`
3. **Implement store methods** in `server/internal/store/postgres.go`
4. **Document in** `docs/db/schema.md`

### Adding a CLI Subcommand

1. **Create file** in `server/internal/cli/{resource}/{command}.go`
2. **Register command** in `server/internal/cli/root.go`
3. **Implement cobra.Command** with flags and RunE
4. **Test via** `make test` and manual `gdhcp-cli` invocation

---

End of FILETREE.md
