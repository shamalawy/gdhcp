# Documentation index

Start with the top-level [../README.md](../README.md) for a quickstart.
**`AMENDMENTS.md` is binding** — where it disagrees with any other doc here
(including `ARCHITECTURE.md` and `CONTRACTS.md`), `AMENDMENTS.md` wins.

## Core references

| Doc | What's in it |
|---|---|
| [AMENDMENTS.md](AMENDMENTS.md) | Binding corrections to the architecture/contracts docs below — read this first when anything else seems to contradict the running code |
| [ARCHITECTURE.md](ARCHITECTURE.md) | The overall system design: components, data flow, HA model, deployment targets |
| [CONTRACTS.md](CONTRACTS.md) | Implementation-level contracts (store interfaces, request/response shapes) used to build the parallel subsystems — read alongside `AMENDMENTS.md` |
| [FILETREE.md](FILETREE.md) | Repository layout reference |
| [db/schema.sql](db/schema.sql) | The database schema as DDL (source for migration `0001_initial_schema.sql`, with the fixes in `AMENDMENTS.md` A5 applied) |
| [api/endpoints.md](api/endpoints.md) | Complete REST API reference: every route, params, request/response shapes, status codes |

## Deployment

| Doc | What's in it |
|---|---|
| [deploy/docker.md](deploy/docker.md) | Docker Compose stack: quick start, why `network_mode: host`, capabilities, two-node HA in Docker |
| [deploy/bare-metal.md](deploy/bare-metal.md) | systemd install via `scripts/install.sh`, including the two-node VIP walkthrough |
| [deploy/kubernetes.md](deploy/kubernetes.md) | Kustomize base + overlays, DaemonSet rationale, why VIP failover needs extra infrastructure on k8s |
| [deploy/troubleshooting.md](deploy/troubleshooting.md) | Runbook for deployment-layer problems (VIP not moving, split-brain, connectivity) across all three targets |

## Operations

| Doc | What's in it |
|---|---|
| [operations/monitoring.md](operations/monitoring.md) | Prometheus metrics reference and example alert rules |
| [operations/backup-recovery.md](operations/backup-recovery.md) | What state matters (it's all in Postgres) and how to back it up / restore it |
| [operations/performance-tuning.md](operations/performance-tuning.md) | Sizing guidance and tuning knobs for larger deployments |

## Design docs

Point-in-time design documents for each subsystem, written before/during
implementation. Where they conflict with the running code, the code (and
`AMENDMENTS.md`) wins.

| Doc | What's in it |
|---|---|
| [design/data-model.md](design/data-model.md) | Data model design: entities, relationships, IPAM hierarchy |
| [design/dhcp-engine.md](design/dhcp-engine.md) | DHCPv4 engine design: packet handling, lease allocation, option inheritance |
| [design/ha-cluster.md](design/ha-cluster.md) | HA clustering design: heartbeat/election mechanics, VIP failover |
| [design/api-cli.md](design/api-cli.md) | REST API and CLI design |
| [design/frontend.md](design/frontend.md) | Angular frontend design |
| [design/deploy-build.md](design/deploy-build.md) | Build and deployment design (the reasoning behind the three deploy targets) |
