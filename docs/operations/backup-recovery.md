# Backup & Recovery

All gdhcp state — scopes, pools, reservations, leases, options, templates,
users, cluster/HA state — lives in PostgreSQL (per `docs/design/deploy-build.md`'s
core principle: the binary itself is stateless and immutable). Backing up
gdhcp means backing up its database; the binary and `config.yaml` are
trivially reproducible from the repo/release artifact and a text file.

## Routine backups: `scripts/backup.sh`

```bash
sudo ./scripts/backup.sh
# or explicitly:
GDHCP_DB_DSN="postgres://gdhcp:secret@localhost:5432/gdhcp?sslmode=disable" \
  GDHCP_BACKUP_DIR=/var/backups/gdhcp \
  GDHCP_BACKUP_KEEP_DAYS=7 \
  ./scripts/backup.sh
```

Runs `pg_dump` (plain SQL, gzip-compressed) to
`$GDHCP_BACKUP_DIR/gdhcp-<UTC timestamp>.sql.gz`, then deletes anything
older than `GDHCP_BACKUP_KEEP_DAYS` (default 7) in that directory. Without
an explicit `GDHCP_DB_DSN`, it reads `database.dsn` out of
`/etc/gdhcp/config.yaml` (or `$GDHCP_CONFIG_FILE`).

**Schedule it** — the script itself doesn't loop:
```bash
# /etc/cron.d/gdhcp-backup — adjust the script path to wherever the repo
# (or just this one script) is checked out on the host.
0 2 * * * gdhcp /opt/gdhcp/scripts/backup.sh >> /var/log/gdhcp/backup.log 2>&1
```
or a systemd timer calling the same script daily.

For Docker/Kubernetes Postgres, run the same script against a reachable DSN
(the container's published port, a `kubectl exec` into the postgres pod, or
point it at the DSN from outside the cluster if `gdhcp-postgres` isn't
`ClusterIP: None`-restricted). For a managed database (RDS, Cloud SQL,
etc.), prefer that provider's native automated snapshots/PITR over
`pg_dump` — `scripts/backup.sh` is meant for self-hosted Postgres.

## Restore

```bash
# 1. stop gdhcp everywhere (bare metal: systemctl stop gdhcp on every node;
#    Docker: docker compose stop gdhcp_server; Kubernetes: scale the
#    DaemonSet to zero via its node labels, or delete the pods)

# 2. restore into a fresh or truncated database
gunzip -c /var/backups/gdhcp/gdhcp-20260101T020000Z.sql.gz | \
  psql "postgres://gdhcp:secret@localhost:5432/gdhcp?sslmode=disable"

# 3. verify the schema_migrations table matches what gdhcp expects, then
#    restart gdhcp — it will refuse to serve if it sees an unmigrated schema
gdhcp migrate --config-file /etc/gdhcp/config.yaml
sudo systemctl start gdhcp   # (or the Docker/Kubernetes equivalent)
```

A restore replaces cluster/heartbeat state too — after restoring an old
backup, expect a fresh election on next start (every server was, from the
database's point of view, last heard from at backup time).

## What backup does NOT cover

- `config.yaml` / `gdhcp.env` — back these up separately (they're small,
  version-controllable if you strip secrets, or just re-derive them from
  `config.yaml.example` + your DB DSN and `jwt_secret`)
- The gdhcp binary itself — pin a release version (`scripts/build/release.sh`
  output, or a container image tag) rather than relying on `dist/gdhcp`
  surviving disk loss
- Anything outside PostgreSQL — there isn't anything; that's the point of
  the single-database design

## Point-in-time recovery

`scripts/backup.sh` produces logical dumps (a snapshot, not continuous
WAL archiving) — recovery is to the moment of the last dump, not to an
arbitrary point in time. If you need finer-grained recovery, enable WAL
archiving on the Postgres instance itself (`archive_mode`, `archive_command`,
or your managed provider's PITR feature) — that's a Postgres-operations
concern orthogonal to gdhcp and out of scope for `scripts/backup.sh`.

## Testing restores

A backup you haven't restored is a hypothesis, not a backup. Periodically:
```bash
createdb gdhcp_restore_test
gunzip -c <latest dump> | psql gdhcp_restore_test
psql gdhcp_restore_test -c "select count(*) from dhcp_leases;"   # sanity check
dropdb gdhcp_restore_test
```
