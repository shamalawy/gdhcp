package db

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey is the session-level advisory lock all gdhcp instances
// take before running migrations, serializing concurrent startups
// (docs/CONTRACTS.md Part 5, docs/ARCHITECTURE.md "Schema Migration
// Locking"). Value is arbitrary but fixed across the fleet: 0x67646863
// ("gdhc" as big-endian ASCII bytes).
const advisoryLockKey int64 = 0x67646863

const migrationsDir = "migrations"

var migrationFileRe = regexp.MustCompile(`^(\d{4})_[A-Za-z0-9_]+\.sql$`)

// migrationFile is one parsed embedded migration.
type migrationFile struct {
	version int64
	name    string // full filename, e.g. "0001_initial_schema.sql"
}

// Migrator runs the embedded SQL migrations against a database, tracking
// what has already been applied in the schema_migrations table.
type Migrator struct {
	pool *pgxpool.Pool
}

// NewMigrator constructs a Migrator bound to pool.
func NewMigrator(pool *pgxpool.Pool) *Migrator {
	return &Migrator{pool: pool}
}

// Run acquires the migration advisory lock, then executes every embedded
// migration file that isn't already recorded in schema_migrations, in
// ascending numeric order. Numbering gaps (e.g. no 0002 file present) are
// tolerated — whatever NNNN_*.sql files exist are run in order. Each
// migration file is expected to wrap itself in BEGIN/COMMIT (docs/CONTRACTS.md
// Part 5 #1); Run executes each file's full text via the simple query
// protocol so those statements are honored as written, then records the
// applied version in its own statement.
func (m *Migrator) Run(ctx context.Context) error {
	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("db: acquire connection for migrations: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("db: acquire migration advisory lock: %w", err)
	}
	defer func() {
		// Best-effort unlock; a dropped connection releases session-level
		// advisory locks automatically, so a failure here is not fatal.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	if err := ensureSchemaMigrationsTable(ctx, conn); err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	files, err := discoverMigrations()
	if err != nil {
		return err
	}

	for _, f := range files {
		if _, ok := applied[f.version]; ok {
			continue
		}
		if err := runMigration(ctx, conn, f); err != nil {
			return fmt.Errorf("db: migration %s: %w", f.name, err)
		}
	}

	return nil
}

// schemaMigrationsDDL matches the schema_migrations table defined in
// migration 0001 (docs/db/schema.sql section 2), made idempotent with IF NOT
// EXISTS so it is safe regardless of whether Run's bootstrap or migration
// 0001 itself creates the table first.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  id BIGSERIAL PRIMARY KEY,
  version BIGINT UNIQUE NOT NULL,
  name VARCHAR(255) NOT NULL,
  executed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  execution_time_ms INTEGER
);
CREATE INDEX IF NOT EXISTS idx_schema_migrations_version ON schema_migrations(version);
`

func ensureSchemaMigrationsTable(ctx context.Context, conn *pgxpool.Conn) error {
	if _, err := conn.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("db: bootstrap schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *pgxpool.Conn) (map[int64]struct{}, error) {
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64]struct{})
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("db: scan schema_migrations: %w", err)
		}
		applied[v] = struct{}{}
	}
	return applied, rows.Err()
}

// discoverMigrations lists and parses the embedded migration files,
// returning them sorted ascending by version. Files that don't match the
// NNNN_name.sql pattern are ignored.
func discoverMigrations() ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("db: list embedded migrations: %w", err)
	}

	files := make([]migrationFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		version, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("db: migration filename %q: %w", e.Name(), err)
		}
		files = append(files, migrationFile{version: version, name: e.Name()})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	return files, nil
}

func runMigration(ctx context.Context, conn *pgxpool.Conn, f migrationFile) error {
	data, err := migrationsFS.ReadFile(path.Join(migrationsDir, f.name))
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	start := time.Now()
	results, err := conn.Conn().PgConn().Exec(ctx, string(data)).ReadAll()
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	for _, res := range results {
		if res.Err != nil {
			return fmt.Errorf("execute: %w", res.Err)
		}
	}
	elapsedMs := int(time.Since(start).Milliseconds())

	if _, err := conn.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, execution_time_ms)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (version) DO NOTHING`,
		f.version, f.name, elapsedMs,
	); err != nil {
		return fmt.Errorf("record schema_migrations row: %w", err)
	}

	return nil
}
