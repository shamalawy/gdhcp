#!/bin/bash
# Creates the gdhcp role and database on a PostgreSQL server. Run once
# before the first `gdhcp migrate`. Requires a Postgres superuser connection
# (defaults to connecting as the local `postgres` role via psql).
#
# Usage:
#   ./scripts/bootstrap-db.sh
#   PGHOST=db.internal PGUSER=postgres GDHCP_DB_PASSWORD=secret ./scripts/bootstrap-db.sh
#
# Env vars (all optional):
#   PGHOST, PGPORT, PGUSER   — connection to the superuser (psql conventions)
#   GDHCP_DB_NAME            — database to create (default: gdhcp)
#   GDHCP_DB_ROLE            — role to create (default: gdhcp)
#   GDHCP_DB_PASSWORD        — role password (default: prompted)
set -euo pipefail

command -v psql >/dev/null 2>&1 || { echo "error: psql not found in PATH" >&2; exit 1; }

DB_NAME="${GDHCP_DB_NAME:-gdhcp}"
DB_ROLE="${GDHCP_DB_ROLE:-gdhcp}"

if [ -z "${GDHCP_DB_PASSWORD:-}" ]; then
    read -r -s -p "Password for new role '${DB_ROLE}': " GDHCP_DB_PASSWORD
    echo ""
fi
[ -n "$GDHCP_DB_PASSWORD" ] || { echo "error: password must not be empty" >&2; exit 1; }

# Identifiers are not user-supplied free text in normal use (env vars set by
# an operator), but quote them defensively anyway.
psql -v ON_ERROR_STOP=1 --set=db_role="$DB_ROLE" --set=db_password="$GDHCP_DB_PASSWORD" <<'SQL'
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'db_role') THEN
        EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', :'db_role', :'db_password');
    ELSE
        EXECUTE format('ALTER ROLE %I LOGIN PASSWORD %L', :'db_role', :'db_password');
    END IF;
END
$$;
SQL

psql -v ON_ERROR_STOP=1 --set=db_name="$DB_NAME" --set=db_role="$DB_ROLE" -tAc \
    "SELECT 1 FROM pg_database WHERE datname = :'db_name'" | grep -q 1 || \
    psql -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"${DB_NAME}\" OWNER \"${DB_ROLE}\" ENCODING 'UTF8'"

psql -v ON_ERROR_STOP=1 -d "$DB_NAME" -c "CREATE EXTENSION IF NOT EXISTS btree_gist;"

echo "Database '${DB_NAME}' and role '${DB_ROLE}' are ready."
echo "DSN: postgres://${DB_ROLE}:<password>@\${PGHOST:-localhost}:\${PGPORT:-5432}/${DB_NAME}?sslmode=disable"
echo "Next: gdhcp migrate --config-file /etc/gdhcp/config.yaml"
