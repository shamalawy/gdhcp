# Docker Deployment

`deploy/docker/` provides a two-container Compose stack for local
development, staging, or a single-node production deployment:
`gdhcp_server` (the DHCP+API+UI binary) and `gdhcp_postgres` (PostgreSQL 16).

## Quick start

```bash
cp deploy/docker/config.yaml.example deploy/docker/config.yaml
# edit deploy/docker/config.yaml — at minimum review dhcp.interfaces (starts
# empty/API-only) and server.jwt_secret

docker compose -f deploy/docker/docker-compose.yml up -d --build
docker compose -f deploy/docker/docker-compose.yml logs -f gdhcp_server
curl http://localhost:8080/api/v1/health
```

Stop with `docker compose -f deploy/docker/docker-compose.yml down` (add
`-v` to also delete the Postgres volume — irreversible).

## Why `network_mode: host`

DHCP clients send DISCOVER as an L2/L3 broadcast to UDP/67. Docker's
default bridge network does not forward broadcast traffic between the host
and a container's network namespace, so a bridged gdhcp container never
sees real client traffic — only a `network_mode: host` container, sharing
the host's interfaces directly, can. This is set in
`deploy/docker/docker-compose.yml` and is not optional for real DHCP
serving; the commented-out `ports:` alternative in that file only works for
the HTTP API, never for DHCP.

One consequence: with the server on host networking, it reaches
`gdhcp_postgres` at `localhost:5432` (not the container's DNS name) —
`deploy/docker/config.yaml.example` and the compose file's
`GDHCP_DATABASE_DSN` override already reflect this.

## Capabilities

`gdhcp_server` needs `CAP_NET_ADMIN` (VIP assignment) and `CAP_NET_RAW`
(gratuitous ARP) at the container level (`cap_add` in the compose file);
`CAP_NET_BIND_SERVICE` for port 67 is instead baked into the image itself
via `setcap` on the binary (see `deploy/docker/Dockerfile`), which is what
lets the container run as the non-root `gdhcp` user rather than root.

## Two-node HA in Docker

Each `gdhcp_server` still needs its own host (host networking means one
container per box) with `ha.enabled: true` and distinct `server_id`/
`node_id`/`server_name`, both pointed at the same `gdhcp_postgres` — either
one node's Postgres reused by the other via `database.dsn`, or a separate
managed instance both containers share. VIP failover between two Docker
hosts works the same way as bare metal (see
[bare-metal.md](bare-metal.md#ha-pair-walkthrough-two-nodes--vip)) since
`network_mode: host` gives the container real access to the host's
interfaces — there is no container-specific VIP mechanism.

## Production notes

- Replace `GDHCP_DB_PASSWORD` (compose file default is a dev password) —
  set it in a `.env` file next to `docker-compose.yml` or your orchestrator's
  secret mechanism, never commit a real one.
- `deploy/docker/config.yaml` is gitignored on purpose; only
  `config.yaml.example` is checked in.
- Back up `gdhcp_postgres`'s volume the same way as any other Postgres
  instance — see [../operations/backup-recovery.md](../operations/backup-recovery.md);
  `scripts/backup.sh` works against a container the same way it does bare
  metal, given a reachable DSN (`docker compose exec gdhcp_postgres` or a
  DSN pointed at the published `127.0.0.1:5432`).
- For genuinely stateless/scaled deployments beyond a single active DHCP
  node, see [kubernetes.md](kubernetes.md) — Compose here targets one
  node (optionally paired for HA) per Docker host, not horizontal scaling.
