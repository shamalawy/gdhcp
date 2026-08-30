#!/bin/bash
# pg_dump backup for the gdhcp database, with 7-day rotation. Intended to run
# from cron/systemd-timer as the gdhcp user (or anyone with DB access).
#
# Usage:
#   ./scripts/backup.sh
#   GDHCP_DB_DSN="postgres://gdhcp:secret@localhost:5432/gdhcp?sslmode=disable" \
#     GDHCP_BACKUP_DIR=/var/backups/gdhcp ./scripts/backup.sh
#
# Env vars:
#   GDHCP_DB_DSN      — full Postgres DSN (default reads it out of
#                        /etc/gdhcp/config.yaml's database.dsn if present)
#   GDHCP_BACKUP_DIR  — where dumps are written (default: /var/backups/gdhcp)
#   GDHCP_BACKUP_KEEP_DAYS — retention in days (default: 7)
set -euo pipefail

command -v pg_dump >/dev/null 2>&1 || { echo "error: pg_dump not found in PATH" >&2; exit 1; }

BACKUP_DIR="${GDHCP_BACKUP_DIR:-/var/backups/gdhcp}"
KEEP_DAYS="${GDHCP_BACKUP_KEEP_DAYS:-7}"
CONFIG_FILE="${GDHCP_CONFIG_FILE:-/etc/gdhcp/config.yaml}"

DSN="${GDHCP_DB_DSN:-}"
if [ -z "$DSN" ] && [ -f "$CONFIG_FILE" ]; then
    DSN="$(sed -nE 's/^\s*dsn:\s*"?([^"[:space:]]+)"?\s*$/\1/p' "$CONFIG_FILE" | head -n1)"
fi
[ -n "$DSN" ] || { echo "error: no DSN — set GDHCP_DB_DSN or ensure database.dsn exists in ${CONFIG_FILE}" >&2; exit 1; }

mkdir -p "$BACKUP_DIR"

TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_FILE="${BACKUP_DIR}/gdhcp-${TIMESTAMP}.sql.gz"

echo "==> dumping to ${OUT_FILE}"
pg_dump --dbname="$DSN" --format=plain --no-owner --no-privileges | gzip -9 > "${OUT_FILE}.tmp"
mv "${OUT_FILE}.tmp" "$OUT_FILE"
echo "==> backup complete: $(du -h "$OUT_FILE" | cut -f1)"

echo "==> pruning backups older than ${KEEP_DAYS} days"
find "$BACKUP_DIR" -maxdepth 1 -name 'gdhcp-*.sql.gz' -mtime "+${KEEP_DAYS}" -print -delete

echo "==> current backups:"
ls -lh "$BACKUP_DIR"/gdhcp-*.sql.gz 2>/dev/null || echo "  (none)"
