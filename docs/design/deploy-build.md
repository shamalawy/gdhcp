# gdhcp: Build & Deployment Design

## Overview

gdhcp is distributed as a single static binary that embeds the Angular frontend, database migrations, and all runtime dependencies. Three deployment targets are supported: bare metal (systemd), Docker Compose, and Kubernetes.

**Core principle**: All application state lives in PostgreSQL. The binary is immutable and requires only a single `config.yaml` to bootstrap and connect to the database. Server identity and cluster membership are registered in the database on first start.

---

## Build

### Directory Structure
```
repo/
  web/                          # Angular frontend
    src/
    angular.json
    package.json
  server/                        # Go backend
    cmd/
      gdhcp/
        main.go                 # Entry point
      gdhcp-cli/
        main.go                 # CLI entry point
    internal/
      server/
      dhcp/
      ipam/
      db/
      config/
    migrations/                 # SQL migration files (numbered, e.g. 001_init.sql)
    go.mod
    go.sum
  Makefile
  deploy/
    docker/
      Dockerfile
      docker-compose.yml
    systemd/
      gdhcp.service
      gdhcp.socket              # (optional, for socket activation)
    k8s/
      kustomization.yaml        # Kustomize base
      base/
        namespace.yaml
        postgres/
          statefulset.yaml
          service.yaml
          configmap.yaml
        gdhcp/
          deployment.yaml
          daemonset.yaml         # (alternative to deployment)
          service.yaml
          configmap.yaml
          secret.yaml
      overlays/
        production/
          kustomization.yaml
        staging/
          kustomization.yaml
  scripts/
    install.sh                  # Bare-metal installer
    uninstall.sh                # Bare-metal uninstaller
  docs/
    design/
      deploy-build.md           # This file
      schema.md
      api.md
      cli.md
```

### Makefile

**Path**: `/home/shereif/code/gdhcp/Makefile`

Target environment: Linux/macOS. Go 1.26+, Node 20+, PostgreSQL (for local dev only).

```makefile
# Version information (from git tag or fallback)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Paths
WEB_DIR := web
WEB_DIST := web/dist
SERVER_DIR := server
MIGRATIONS_DIR := server/migrations

# Output
BINARY_NAME := gdhcp
BINARY_CLI_NAME := gdhcp-cli
DIST_DIR := dist

# Go build flags for ldflags
LDFLAGS := -ldflags "\
  -X 'main.Version=$(VERSION)' \
  -X 'main.BuildTime=$(BUILD_TIME)' \
  -X 'main.GitCommit=$(GIT_COMMIT)' \
  -w -s"

.PHONY: help
help:
	@echo "gdhcp build targets:"
	@echo "  make build          - Build server binary (requires web/dist)"
	@echo "  make web            - Build Angular frontend"
	@echo "  make all            - Build web + server (full release binary)"
	@echo "  make dist           - Build all artifacts to dist/"
	@echo "  make clean          - Remove build artifacts"
	@echo "  make test           - Run tests"
	@echo "  make dev            - Build server in dev mode (no optimization)"

.PHONY: web
web:
	cd $(WEB_DIR) && npm ci && npm run build

.PHONY: build
build: $(WEB_DIST)
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=1 go build $(LDFLAGS) \
	  -o $(DIST_DIR)/$(BINARY_NAME) \
	  ./$(SERVER_DIR)/cmd/gdhcp

.PHONY: build-cli
build-cli:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=1 go build $(LDFLAGS) \
	  -o $(DIST_DIR)/$(BINARY_CLI_NAME) \
	  ./$(SERVER_DIR)/cmd/gdhcp-cli

.PHONY: all
all: web build build-cli
	@echo "Build complete: $(DIST_DIR)/$(BINARY_NAME) and $(DIST_DIR)/$(BINARY_CLI_NAME)"

.PHONY: dist
dist: all
	# Copy migrations and config templates
	mkdir -p $(DIST_DIR)/migrations
	cp $(MIGRATIONS_DIR)/*.sql $(DIST_DIR)/migrations/
	cp config.yaml.example $(DIST_DIR)/config.yaml.example
	@echo "Distribution artifacts ready in $(DIST_DIR)/"

.PHONY: dev
dev: web
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=1 go build -o $(DIST_DIR)/$(BINARY_NAME) \
	  ./$(SERVER_DIR)/cmd/gdhcp

.PHONY: test
test:
	go test -v -race -coverprofile=coverage.out ./$(SERVER_DIR)/...
	cd $(WEB_DIR) && npm test

.PHONY: clean
clean:
	rm -rf $(DIST_DIR) $(WEB_DIST)
	go clean -cache

.PHONY: lint
lint:
	golangci-lint run ./$(SERVER_DIR)/...
	cd $(WEB_DIR) && npm run lint

.PHONY: fmt
fmt:
	gofmt -s -w ./$(SERVER_DIR)
	cd $(WEB_DIR) && npm run format
```

### Go Embedding (Migrations & Web Assets)

**File**: `/home/shereif/code/gdhcp/server/cmd/gdhcp/main.go`

```go
package main

import (
	"embed"
	"fmt"

	"gdhcp/server/internal/server"
)

// Embed migrations and web assets into the binary
//go:embed ../../migrations/*.sql
var Migrations embed.FS

//go:embed ../../../web/dist/*
var WebAssets embed.FS

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	app := server.New(
		server.WithVersion(Version, BuildTime, GitCommit),
		server.WithMigrations(Migrations),
		server.WithWebAssets(WebAssets),
	)
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
```

---

## Configuration

### config.yaml Schema

**Path**: `/etc/gdhcp/config.yaml` (production) or `config.yaml` (development)

Single YAML file. All other configuration (scopes, pools, templates, options, etc.) is managed via the API/CLI/GUI and persisted in the database.

```yaml
# gdhcp server configuration
# Database connection
db:
  dsn: "postgres://gdhcp:password@localhost:5432/gdhcp?sslmode=disable"
  # Or individual components:
  # host: localhost
  # port: 5432
  # user: gdhcp
  # password: password
  # database: gdhcp
  # sslmode: disable

# Server identity and cluster
server:
  name: "server1"                    # Unique node name (auto-registered in DB)
  node_id: ""                        # (auto-generated if empty)
  cluster_name: "production"         # Cluster this server belongs to

# HTTP API & Web UI
http:
  listen_addr: "0.0.0.0"
  listen_port: 8080
  tls:
    enabled: false
    cert_file: ""
    key_file: ""

# DHCP server
dhcp:
  listen_addr: "0.0.0.0"
  listen_port: 67
  # Host network mode (Docker/K8s) or direct bind (bare metal)
  # Set to "host" when running in containers/K8s with hostNetwork

# Logging
log:
  level: "info"                       # debug, info, warn, error
  format: "json"                      # json or text
  output: "stdout"                    # stdout, file, or both
  file: "/var/log/gdhcp/server.log"

# Optional: high-availability (VIP)
ha:
  enabled: false
  virtual_ip: "192.168.1.254"
  virtual_mac: ""                     # (auto-generated if empty)
  # Cluster members (from DB, but can be overridden here for bootstrap)
  # members:
  #   - server1
  #   - server2

# Feature flags (optional, all default to enabled)
features:
  vrf: true
  reservations: true
  templates: true
  api: true
  web_ui: true
```

**Environment variables** (optional override of config.yaml):
- `GDHCP_DB_DSN`
- `GDHCP_SERVER_NAME`
- `GDHCP_HTTP_LISTEN_PORT`
- `GDHCP_DHCP_LISTEN_PORT`
- `GDHCP_LOG_LEVEL`

---

## Bare Metal Deployment

### install.sh

**Path**: `/home/shereif/code/gdhcp/scripts/install.sh`

Installs gdhcp as a systemd service. Requires root.

```bash
#!/bin/bash
set -e

VERSION="${1:-latest}"
BINARY_URL="https://github.com/your-org/gdhcp/releases/download/${VERSION}/gdhcp"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/gdhcp"
DATA_DIR="/var/lib/gdhcp"
LOG_DIR="/var/log/gdhcp"
SYSTEMD_DIR="/etc/systemd/system"

echo "Installing gdhcp ${VERSION}..."

# Create gdhcp user (system user, no login, no home)
if ! id -u gdhcp >/dev/null 2>&1; then
    useradd -r -s /usr/sbin/nologin -d /var/lib/gdhcp gdhcp
    echo "Created gdhcp system user"
fi

# Create directories
mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
chown gdhcp:gdhcp "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
chmod 750 "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"

# Download binary
echo "Downloading binary from ${BINARY_URL}..."
curl -fsSL "$BINARY_URL" -o "${INSTALL_DIR}/gdhcp"
chmod 755 "${INSTALL_DIR}/gdhcp"

# Install config template (if not exists)
if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    cat > "$CONFIG_DIR/config.yaml" <<EOF
db:
  dsn: "postgres://gdhcp:changeme@localhost:5432/gdhcp?sslmode=disable"

server:
  name: "$(hostname)"
  cluster_name: "default"

http:
  listen_addr: "0.0.0.0"
  listen_port: 8080

dhcp:
  listen_addr: "0.0.0.0"
  listen_port: 67

log:
  level: "info"
  format: "json"
  output: "stdout"
EOF
    chown gdhcp:gdhcp "$CONFIG_DIR/config.yaml"
    chmod 600 "$CONFIG_DIR/config.yaml"
    echo "Created default config at ${CONFIG_DIR}/config.yaml - PLEASE EDIT IT"
fi

# Install systemd service
cat > "${SYSTEMD_DIR}/gdhcp.service" <<'EOF'
[Unit]
Description=gdhcp - High-Availability DHCP Server with IPAM
Documentation=https://github.com/your-org/gdhcp
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=gdhcp
Group=gdhcp
Restart=on-failure
RestartSec=5s

# Capabilities for DHCP and network operations
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE
SecureBits=keep-caps

# Security hardening
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
NoNewPrivileges=yes
ReadWritePaths=/etc/gdhcp /var/lib/gdhcp /var/log/gdhcp

# Start command
ExecStart=/usr/local/bin/gdhcp serve --config-file /etc/gdhcp/config.yaml

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=gdhcp

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable gdhcp.service

echo ""
echo "Installation complete!"
echo "1. Edit ${CONFIG_DIR}/config.yaml with your database and network settings"
echo "2. Ensure PostgreSQL is running and the database is initialized"
echo "3. Start gdhcp with: systemctl start gdhcp"
echo "4. Check status with: systemctl status gdhcp"
echo "5. View logs with: journalctl -u gdhcp -f"
```

### uninstall.sh

**Path**: `/home/shereif/code/gdhcp/scripts/uninstall.sh`

Removes gdhcp service and files.

```bash
#!/bin/bash
set -e

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/gdhcp"
SYSTEMD_DIR="/etc/systemd/system"

echo "Uninstalling gdhcp..."

# Stop and disable service
systemctl stop gdhcp.service 2>/dev/null || true
systemctl disable gdhcp.service 2>/dev/null || true

# Remove systemd unit
rm -f "${SYSTEMD_DIR}/gdhcp.service"
systemctl daemon-reload

# Remove binary
rm -f "${INSTALL_DIR}/gdhcp"
rm -f "${INSTALL_DIR}/gdhcp-cli"

# Optionally preserve config and data for re-installation
echo ""
echo "Uninstallation complete!"
echo "Config and data preserved at: ${CONFIG_DIR}"
echo "To remove config and data (DESTRUCTIVE), run:"
echo "  sudo rm -rf ${CONFIG_DIR} /var/lib/gdhcp /var/log/gdhcp"
```

### systemd Service Unit

**Path**: `/home/shereif/code/gdhcp/deploy/systemd/gdhcp.service`

(Already included in install.sh above, but also provided standalone for manual installation)

Key points:
- **AmbientCapabilities**: CAP_NET_ADMIN (raw socket, interface manipulation), CAP_NET_RAW (raw sockets), CAP_NET_BIND_SERVICE (bind port 67)
- **Type=simple**: Single-process service
- **Restart=on-failure**: Auto-restart on exit with non-zero status
- **ProtectSystem=strict**: Read-only root filesystem (except specific paths)
- **ReadWritePaths**: /etc/gdhcp, /var/lib/gdhcp, /var/log/gdhcp

---

## Docker Deployment

### Dockerfile

**Path**: `/home/shereif/code/gdhcp/deploy/docker/Dockerfile`

Multi-stage build: Node for frontend, Go for backend, distroless/alpine for final image.

```dockerfile
# Stage 1: Build Angular frontend
FROM node:20-alpine AS web-builder

WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# Stage 2: Build Go backend
FROM golang:1.26-alpine AS go-builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY server/ ./server/
COPY deploy/ ./deploy/
# Copy built frontend from Stage 1
COPY --from=web-builder /app/web/dist ./web/dist
COPY server/migrations ./server/migrations/

# Build the binary with version info
ARG VERSION=dev
ARG BUILD_TIME=unknown
ARG GIT_COMMIT=unknown

RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
  -ldflags="-X 'main.Version=${VERSION}' -X 'main.BuildTime=${BUILD_TIME}' -X 'main.GitCommit=${GIT_COMMIT}' -w -s" \
  -o /gdhcp ./server/cmd/gdhcp && \
  CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
  -ldflags="-X 'main.Version=${VERSION}' -X 'main.BuildTime=${BUILD_TIME}' -X 'main.GitCommit=${GIT_COMMIT}' -w -s" \
  -o /gdhcp-cli ./server/cmd/gdhcp-cli

# Stage 3: Runtime image (distroless-alpine for minimal size and attack surface)
FROM alpine:3.20

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tini

# Create gdhcp user
RUN addgroup -g 1000 gdhcp && \
    adduser -D -u 1000 -G gdhcp gdhcp

# Copy binaries from builder
COPY --from=go-builder /gdhcp /usr/local/bin/gdhcp
COPY --from=go-builder /gdhcp-cli /usr/local/bin/gdhcp-cli

# Default config location
RUN mkdir -p /etc/gdhcp /var/lib/gdhcp /var/log/gdhcp && \
    chown -R gdhcp:gdhcp /etc/gdhcp /var/lib/gdhcp /var/log/gdhcp

USER gdhcp:gdhcp
WORKDIR /etc/gdhcp

# Health check
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD gdhcp-cli health || exit 1

# Use tini for proper signal handling
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/gdhcp", "serve", "--config-file", "/etc/gdhcp/config.yaml"]

EXPOSE 8080 67/udp
```

### docker-compose.yml

**Path**: `/home/shereif/code/gdhcp/deploy/docker/docker-compose.yml`

Services: `gdhcp_postgres` (database), `gdhcp_server` (DHCP+API).

```yaml
version: '3.9'

services:
  gdhcp_postgres:
    image: postgres:16-alpine
    container_name: gdhcp_postgres
    environment:
      POSTGRES_DB: gdhcp
      POSTGRES_USER: gdhcp
      POSTGRES_PASSWORD: gdhcp_dev_password  # CHANGE IN PRODUCTION
      POSTGRES_INITDB_ARGS: "--encoding=UTF8"
    volumes:
      - postgres_data:/var/lib/postgresql/data
      # Optional: pre-populated SQL init script
      # - ./init.sql:/docker-entrypoint-initdb.d/init.sql
    ports:
      - "127.0.0.1:5432:5432"  # Localhost only
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U gdhcp"]
      interval: 10s
      timeout: 3s
      retries: 3
    networks:
      - gdhcp_net

  gdhcp_server:
    build:
      context: ../..
      dockerfile: deploy/docker/Dockerfile
      args:
        VERSION: ${GDHCP_VERSION:-dev}
        BUILD_TIME: ${BUILD_TIME:-unknown}
        GIT_COMMIT: ${GIT_COMMIT:-unknown}
    container_name: gdhcp_server
    depends_on:
      gdhcp_postgres:
        condition: service_healthy
    environment:
      GDHCP_DB_DSN: "postgres://gdhcp:gdhcp_dev_password@gdhcp_postgres:5432/gdhcp?sslmode=disable"
      GDHCP_LOG_LEVEL: "info"
    volumes:
      - gdhcp_config:/etc/gdhcp
      - gdhcp_data:/var/lib/gdhcp
      - gdhcp_logs:/var/log/gdhcp
    # CRITICAL: Host network mode required for DHCP broadcast packets
    # DHCP clients send broadcast packets to port 67/UDP which only work
    # on the host network, not container networks (no broadcast across bridge)
    network_mode: "host"
    # Alternative to host mode (less reliable):
    # ports:
    #   - "67:67/udp"
    #   - "8080:8080"
    cap_add:
      - NET_ADMIN
      - NET_RAW
      - NET_BIND_SERVICE
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/api/health"]
      interval: 10s
      timeout: 3s
      retries: 3

volumes:
  postgres_data:
    driver: local
  gdhcp_config:
    driver: local
  gdhcp_data:
    driver: local
  gdhcp_logs:
    driver: local

networks:
  gdhcp_net:
    driver: bridge
```

**Build & Run**:
```bash
# Build images
docker compose -f deploy/docker/docker-compose.yml build

# Start services
docker compose -f deploy/docker/docker-compose.yml up -d

# View logs
docker compose -f deploy/docker/docker-compose.yml logs -f gdhcp_server

# Stop and remove
docker compose -f deploy/docker/docker-compose.yml down -v
```

**Production Considerations**:
- Replace hardcoded password with secrets (Docker Secrets or external vault)
- Use persistent volumes (e.g., AWS EBS, NFS) for data durability
- Configure PostgreSQL backups
- For multi-node HA, each gdhcp_server instance needs its own container with VIP routing setup (requires external orchestration)

---

## Kubernetes Deployment

### Rationale: Kustomize vs Helm

**Decision: Kustomize** (over Helm)

Reasons:
1. Simpler declarative model: YAML overlays with strategic merge patches
2. No templating language to learn (Go templates in Helm are verbose)
3. Easier to audit and version-control; no generated code
4. Better suited for multi-environment deployments (prod/staging/dev with overlays)
5. Integrates natively with kubectl (v1.14+)

Helm may be added later if complex orchestration (e.g., automatic rollback, canary deployments) is needed, but Kustomize is sufficient for initial deployment.

### Directory Structure

```
deploy/k8s/
├── kustomization.yaml          # Root kustomization (references base)
├── base/
│   ├── kustomization.yaml
│   ├── namespace.yaml
│   ├── configmap.yaml           # Shared config template
│   ├── postgres/
│   │   ├── kustomization.yaml
│   │   ├── statefulset.yaml     # PostgreSQL StatefulSet
│   │   ├── service.yaml
│   │   └── pvc.yaml             # PersistentVolumeClaim
│   └── gdhcp/
│       ├── kustomization.yaml
│       ├── deployment.yaml      # or daemonset.yaml (see below)
│       ├── service.yaml         # ClusterIP for API/Web UI
│       ├── configmap.yaml
│       ├── secret.yaml          # Database credentials, TLS certs
│       └── rbac.yaml            # ServiceAccount, ClusterRole, RoleBinding
└── overlays/
    ├── production/
    │   └── kustomization.yaml
    ├── staging/
    │   └── kustomization.yaml
    └── dev/
        └── kustomization.yaml
```

### Base Kustomization

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/kustomization.yaml`

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: gdhcp

# Common metadata applied to all resources
commonLabels:
  app.kubernetes.io/name: gdhcp
  app.kubernetes.io/instance: gdhcp
  app.kubernetes.io/version: 1.0.0

commonAnnotations:
  app.kubernetes.io/managed-by: kustomize

# Image tag replacement (for version updates)
images:
  - name: gdhcp
    newTag: latest  # Override in overlays

# Resource composition
resources:
  - namespace.yaml
  - postgres/kustomization.yaml
  - gdhcp/kustomization.yaml

# Replicas (can be overridden per overlay)
replicas:
  - name: gdhcp
    count: 1
```

### Namespace

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/namespace.yaml`

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: gdhcp
  labels:
    app.kubernetes.io/name: gdhcp
```

### PostgreSQL StatefulSet (Optional)

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/postgres/statefulset.yaml`

If running PostgreSQL in-cluster. For production, consider external managed PostgreSQL (RDS, Cloud SQL, Azure Database).

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: gdhcp-postgres
  namespace: gdhcp
  labels:
    app.kubernetes.io/component: database
spec:
  serviceName: gdhcp-postgres
  replicas: 1  # Single replica for simplicity; add Patroni/etcd for HA
  selector:
    matchLabels:
      app.kubernetes.io/component: database
  template:
    metadata:
      labels:
        app.kubernetes.io/component: database
    spec:
      securityContext:
        fsGroup: 999
      containers:
      - name: postgres
        image: postgres:16-alpine
        ports:
        - containerPort: 5432
          name: postgres
        env:
        - name: POSTGRES_DB
          value: gdhcp
        - name: POSTGRES_USER
          valueFrom:
            secretKeyRef:
              name: gdhcp-db-credentials
              key: username
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: gdhcp-db-credentials
              key: password
        - name: PGDATA
          value: /var/lib/postgresql/data/pgdata
        volumeMounts:
        - name: pgdata
          mountPath: /var/lib/postgresql/data
        livenessProbe:
          exec:
            command:
            - /bin/sh
            - -c
            - pg_isready -U $(POSTGRES_USER)
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          exec:
            command:
            - /bin/sh
            - -c
            - pg_isready -U $(POSTGRES_USER)
          initialDelaySeconds: 5
          periodSeconds: 5
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
  volumeClaimTemplates:
  - metadata:
      name: pgdata
    spec:
      accessModes: [ "ReadWriteOnce" ]
      storageClassName: standard
      resources:
        requests:
          storage: 10Gi
```

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/postgres/service.yaml`

```yaml
apiVersion: v1
kind: Service
metadata:
  name: gdhcp-postgres
  namespace: gdhcp
  labels:
    app.kubernetes.io/component: database
spec:
  clusterIP: None  # Headless service for StatefulSet
  selector:
    app.kubernetes.io/component: database
  ports:
  - port: 5432
    targetPort: 5432
    name: postgres
```

### gdhcp Deployment (Primary)

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/deployment.yaml`

Deployment is preferred for stateless DHCP+API servers (horizontal scaling, rolling updates). For HA, use a secondary Deployment with VIP failover managed externally (e.g., keepalived, AWS Route 53 health checks).

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gdhcp
  namespace: gdhcp
spec:
  replicas: 1  # Override per overlay for HA
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  selector:
    matchLabels:
      app.kubernetes.io/name: gdhcp
      app.kubernetes.io/component: server
  template:
    metadata:
      labels:
        app.kubernetes.io/name: gdhcp
        app.kubernetes.io/component: server
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
    spec:
      serviceAccountName: gdhcp
      securityContext:
        fsGroup: 1000
      hostNetwork: true  # CRITICAL: Required for DHCP broadcast
      hostPID: false
      hostIPC: false
      dnsPolicy: ClusterFirstWithHostNet
      nodeSelector:
        gdhcp.io/dhcp-server: "true"  # Deploy only on DHCP-serving nodes
      tolerations:
      - key: gdhcp.io/dhcp-server
        operator: Equal
        value: "true"
        effect: NoSchedule
      containers:
      - name: gdhcp
        image: gdhcp:latest
        imagePullPolicy: IfNotPresent
        ports:
        - containerPort: 8080
          name: http
          protocol: TCP
        - containerPort: 67
          name: dhcp
          protocol: UDP
          hostPort: 67
        env:
        - name: GDHCP_DB_DSN
          valueFrom:
            secretKeyRef:
              name: gdhcp-db-credentials
              key: dsn
        - name: GDHCP_SERVER_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName  # Use node name for server identity
        - name: GDHCP_HTTP_LISTEN_PORT
          value: "8080"
        - name: GDHCP_LOG_LEVEL
          valueFrom:
            configMapKeyRef:
              name: gdhcp-config
              key: log_level
        configMap:
          name: gdhcp-config
          items:
          - key: config.yaml
            path: config.yaml
        volumeMounts:
        - name: config
          mountPath: /etc/gdhcp
          readOnly: true
        securityContext:
          allowPrivilegeEscalation: true  # Required for CAP_NET_ADMIN
          capabilities:
            add:
            - NET_ADMIN
            - NET_RAW
            - NET_BIND_SERVICE
            drop:
            - ALL
          readOnlyRootFilesystem: true
        livenessProbe:
          httpGet:
            path: /api/health
            port: http
          initialDelaySeconds: 10
          periodSeconds: 10
          timeoutSeconds: 3
        readinessProbe:
          httpGet:
            path: /api/health
            port: http
          initialDelaySeconds: 5
          periodSeconds: 5
          timeoutSeconds: 2
        resources:
          requests:
            memory: "256Mi"
            cpu: "500m"
          limits:
            memory: "512Mi"
            cpu: "1000m"
      volumes:
      - name: config
        configMap:
          name: gdhcp-config
```

### gdhcp DaemonSet (Alternative)

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/daemonset.yaml`

Alternative to Deployment. Use DaemonSet if gdhcp must run on every node (not typical; only if every node is a DHCP server).

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: gdhcp
  namespace: gdhcp
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: gdhcp
      app.kubernetes.io/component: server
  template:
    metadata:
      labels:
        app.kubernetes.io/name: gdhcp
        app.kubernetes.io/component: server
    spec:
      serviceAccountName: gdhcp
      hostNetwork: true
      hostPID: false
      hostIPC: false
      dnsPolicy: ClusterFirstWithHostNet
      nodeSelector:
        gdhcp.io/dhcp-server: "true"
      tolerations:
      - operator: Exists
      containers:
      - name: gdhcp
        image: gdhcp:latest
        imagePullPolicy: IfNotPresent
        ports:
        - containerPort: 8080
          hostPort: 8080
          name: http
        - containerPort: 67
          hostPort: 67
          name: dhcp
          protocol: UDP
        env:
        - name: GDHCP_DB_DSN
          valueFrom:
            secretKeyRef:
              name: gdhcp-db-credentials
              key: dsn
        - name: GDHCP_SERVER_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        volumeMounts:
        - name: config
          mountPath: /etc/gdhcp
          readOnly: true
        securityContext:
          allowPrivilegeEscalation: true
          capabilities:
            add:
            - NET_ADMIN
            - NET_RAW
            - NET_BIND_SERVICE
            drop:
            - ALL
          readOnlyRootFilesystem: true
        livenessProbe:
          httpGet:
            path: /api/health
            port: http
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /api/health
            port: http
          initialDelaySeconds: 5
          periodSeconds: 5
        resources:
          requests:
            memory: "256Mi"
            cpu: "500m"
          limits:
            memory: "512Mi"
            cpu: "1000m"
      volumes:
      - name: config
        configMap:
          name: gdhcp-config
```

### Service

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/service.yaml`

ClusterIP service for API and web UI. DHCP traffic uses UDP directly on port 67 (hostPort binding).

```yaml
apiVersion: v1
kind: Service
metadata:
  name: gdhcp-api
  namespace: gdhcp
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: gdhcp
    app.kubernetes.io/component: server
  ports:
  - name: http
    port: 8080
    targetPort: 8080
    protocol: TCP
```

### ConfigMap

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/configmap.yaml`

Default configuration (can be overridden per overlay).

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gdhcp-config
  namespace: gdhcp
data:
  config.yaml: |
    db:
      dsn: "${GDHCP_DB_DSN}"
    server:
      name: "$(GDHCP_SERVER_NAME)"
      cluster_name: "kubernetes"
    http:
      listen_addr: "0.0.0.0"
      listen_port: 8080
    dhcp:
      listen_addr: "0.0.0.0"
      listen_port: 67
    log:
      level: "info"
      format: "json"
      output: "stdout"
  log_level: "info"
```

### Secret

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/secret.yaml`

Database credentials and TLS (if using HTTPS).

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: gdhcp-db-credentials
  namespace: gdhcp
type: Opaque
stringData:
  username: gdhcp
  password: changeme  # Override in overlays or use external secret manager
  dsn: "postgres://gdhcp:changeme@gdhcp-postgres:5432/gdhcp?sslmode=disable"
```

**Security Note**: Use Kubernetes Secrets via external management (e.g., Sealed Secrets, External Secrets Operator) or a vault service (HashiCorp Vault, AWS Secrets Manager) in production. Never commit plaintext credentials.

### RBAC

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/rbac.yaml`

ServiceAccount, ClusterRole (if needed), and RoleBinding.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: gdhcp
  namespace: gdhcp
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: gdhcp
rules:
# Allow reading pod metadata (for node selection, debugging)
- apiGroups: [""]
  resources: ["pods", "nodes"]
  verbs: ["get", "list", "watch"]
# Allow reading ConfigMaps (for configuration)
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: gdhcp
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: gdhcp
subjects:
- kind: ServiceAccount
  name: gdhcp
  namespace: gdhcp
```

### Production Overlay

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/overlays/production/kustomization.yaml`

Override replicas, resource limits, image tags, and database settings for production.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: gdhcp

bases:
  - ../../base

namePrefix: prod-

commonLabels:
  environment: production

images:
  - name: gdhcp
    newTag: v1.0.0  # Pin production image to release tag

replicas:
  - name: gdhcp
    count: 2  # 2-node HA cluster

patchesStrategicMerge:
  - deployment-patch.yaml

secretGenerator:
  - name: gdhcp-db-credentials
    envs:
      - .env.secret  # External file with production credentials
    behavior: replace

configMapGenerator:
  - name: gdhcp-config
    literals:
      - log_level=warn
    behavior: replace
```

**Path**: `/home/shereif/code/gdhcp/deploy/k8s/overlays/production/deployment-patch.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gdhcp
spec:
  template:
    spec:
      containers:
      - name: gdhcp
        resources:
          requests:
            memory: "512Mi"
            cpu: "1000m"
          limits:
            memory: "1Gi"
            cpu: "2000m"
```

### Deploy & Update

```bash
# Apply base (with explicit namespace)
kubectl apply -k deploy/k8s/base/

# Apply production overlay
kubectl apply -k deploy/k8s/overlays/production/

# View deployed resources
kubectl get all -n gdhcp

# Check logs
kubectl logs -n gdhcp deployment/gdhcp -f

# Port-forward to test API locally
kubectl port-forward -n gdhcp svc/gdhcp-api 8080:8080

# Rollout restart (force re-fetch of image)
kubectl rollout restart deployment/gdhcp -n gdhcp

# Update image tag (via overlays)
# 1. Edit overlays/production/kustomization.yaml (images.newTag)
# 2. kubectl apply -k deploy/k8s/overlays/production/
```

---

## Kubernetes: Important Caveats & Design Decisions

### 1. hostNetwork: true (Required)

**Why**: DHCP relies on layer-2 broadcast packets. Kubernetes CNI (Flannel, Calico, Weave) does not forward layer-2 broadcasts between containers/pods. With `hostNetwork: true`, the pod shares the host's network namespace and can directly bind to UDP port 67, receiving broadcast packets on the host's interfaces.

**Trade-off**: Pod is not isolated from the host network; it can access all host interfaces. Acceptable for infrastructure components (DHCP), unacceptable for untrusted workloads.

### 2. Node Selection & Taints/Tolerations

gdhcp must run only on nodes that have a layer-2 connection to DHCP clients (typically management network or OOB network). Use node labels and taints:

```bash
# Label nodes eligible to run DHCP
kubectl label node node-1 gdhcp.io/dhcp-server=true
kubectl label node node-2 gdhcp.io/dhcp-server=true

# Taint nodes to prevent other workloads
kubectl taint nodes node-1 gdhcp.io/dhcp-server=true:NoSchedule
```

Deployment manifest uses `nodeSelector` and `tolerations` to enforce this.

### 3. Virtual IP (VIP) in Kubernetes

For HA with a floating VIP:

**Limitation**: Kubernetes does not natively support VIP failover within a single node pool. Options:

1. **External LB (Recommended)**: Use cloud provider's load balancer (AWS NLB, GCP Load Balancer) with health checks to route DHCP traffic. Requires DNS A record pointing to LB.

2. **Keepalived (Manual)**: Run keepalived on each gdhcp node (inside or outside container) to manage VRRP VIP. Requires L2 adjacency and custom setup.

3. **Cloud-Native HA**: Use managed DHCP services (AWS DHCP Option Sets) for each VPC/subnet instead of maintaining servers.

**Recommendation for gdhcp-in-K8s**: Deploy 1 gdhcp Deployment (scale to N replicas) and use external health checks + DNS round-robin. If VIP is required, keep DHCP on bare metal (simpler, more reliable) and use K8s for API/Web UI only.

### 4. Persistent Volumes

PostgreSQL data is stored on a PersistentVolumeClaim (PVC). In Kubernetes, specify `storageClassName` to use the cluster's storage (local, EBS, NFS, etc.). For production:

```yaml
storageClassName: fast-ssd  # Use your cluster's storage class
resources:
  requests:
    storage: 50Gi           # Adjust per your lease pool size
```

Backups must be handled separately (automated snapshots, WAL archiving to S3, etc.).

### 5. DNS Resolution

gdhcp must resolve `gdhcp-postgres` to the PostgreSQL service IP (within K8s) or external database hostname. Use:

- Kubernetes DNS (default): `gdhcp-postgres.gdhcp.svc.cluster.local`
- External DB: Provide full hostname in `GDHCP_DB_DSN`

### 6. Image Pull Policy

For development/testing:
```yaml
imagePullPolicy: IfNotPresent
```

For production, always use a container registry and specify explicit image tags:
```yaml
image: my-registry.com/gdhcp:v1.0.0
imagePullPolicy: IfNotPresent  # or Always for always fresh
```

---

## Version Stamping

Version information is injected at build time via Go `ldflags` and available at runtime:

```bash
$ gdhcp --version
gdhcp version v1.2.3 (built 2026-08-29T10:30:00Z, commit abc123d)
```

Exposed via API:
```bash
$ curl http://localhost:8080/api/version
{
  "version": "v1.2.3",
  "buildTime": "2026-08-29T10:30:00Z",
  "gitCommit": "abc123d"
}
```

---

## Build Artifacts

### Makefile Targets

| Target | Output | Purpose |
|--------|--------|---------|
| `make web` | `web/dist/` | Build Angular frontend |
| `make build` | `dist/gdhcp` | Build Go binary (requires web/dist) |
| `make build-cli` | `dist/gdhcp-cli` | Build CLI binary |
| `make all` | `dist/gdhcp`, `dist/gdhcp-cli` | Build both binaries |
| `make dist` | `dist/` with migrations | Full distribution package |
| `make test` | stdout | Run all tests |
| `make clean` | — | Remove build artifacts |
| `make lint` | stdout | Run linters |

### Release Process

1. **Tag the release**:
   ```bash
   git tag -a v1.0.0 -m "Release v1.0.0"
   git push origin v1.0.0
   ```

2. **Build artifacts**:
   ```bash
   VERSION=v1.0.0 make dist
   ```

3. **Docker image** (in CI/CD):
   ```bash
   docker build -t my-registry/gdhcp:v1.0.0 \
     --build-arg VERSION=v1.0.0 \
     -f deploy/docker/Dockerfile .
   docker push my-registry/gdhcp:v1.0.0
   ```

4. **Kubernetes deployment**:
   ```bash
   cd deploy/k8s/overlays/production
   kustomize edit set image gdhcp=my-registry/gdhcp:v1.0.0
   kubectl apply -k .
   ```

---

## Summary of File Paths

### Build & Source
- `/home/shereif/code/gdhcp/Makefile` — Build orchestration
- `/home/shereif/code/gdhcp/server/cmd/gdhcp/main.go` — Server binary entry point
- `/home/shereif/code/gdhcp/server/cmd/gdhcp-cli/main.go` — CLI binary entry point
- `/home/shereif/code/gdhcp/server/migrations/*.sql` — Database migrations
- `/home/shereif/code/gdhcp/web/` — Angular frontend (built to `web/dist/`)

### Bare Metal Deployment
- `/home/shereif/code/gdhcp/scripts/install.sh` — Installer
- `/home/shereif/code/gdhcp/scripts/uninstall.sh` — Uninstaller
- `/home/shereif/code/gdhcp/deploy/systemd/gdhcp.service` — systemd unit file
- `/etc/gdhcp/config.yaml` — Runtime config (on deployed system)

### Docker
- `/home/shereif/code/gdhcp/deploy/docker/Dockerfile` — Multi-stage build
- `/home/shereif/code/gdhcp/deploy/docker/docker-compose.yml` — Compose manifest

### Kubernetes
- `/home/shereif/code/gdhcp/deploy/k8s/base/kustomization.yaml` — Base kustomization
- `/home/shereif/code/gdhcp/deploy/k8s/base/namespace.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/base/postgres/statefulset.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/base/postgres/service.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/deployment.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/daemonset.yaml` (alternative)
- `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/service.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/configmap.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/secret.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/base/gdhcp/rbac.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/overlays/production/kustomization.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/overlays/staging/kustomization.yaml`
- `/home/shereif/code/gdhcp/deploy/k8s/overlays/dev/kustomization.yaml`

### Configuration
- `/etc/gdhcp/config.yaml` — Single server configuration (production)
- `config.yaml` — Configuration in repo root (development)

---

## Next Steps for Implementation

1. **Create the Makefile** with web + Go build stages, version stamping
2. **Write Go main.go** with embed directives for migrations and web assets
3. **Create install.sh/uninstall.sh** with systemd integration and capability setup
4. **Build Dockerfile** with Node + Go multi-stage build
5. **Create docker-compose.yml** with explicit container names, host network mode
6. **Create Kubernetes manifests** (base + overlays) with node selection, RBAC, capabilities
7. **Document config.yaml schema** and environment variable overrides
8. **Set up CI/CD** (GitHub Actions, GitLab CI) to build, test, and release artifacts
9. **Create a quick-start guide** for each deployment target
10. **Test HA failover** (bare metal VIP, K8s dual-node, Docker container restart)
