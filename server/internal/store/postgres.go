// Package store's PostgreSQL implementation. postgres.go holds the
// Postgres type itself, its constructor, shared SQL/error-mapping helpers
// used across the postgres_*.go files, and the small ConfigChangeStore
// (LISTEN/NOTIFY) primitive that ties CRUD writes to config-change
// propagation (docs/CONTRACTS.md Part 7, amendment A13's ClusterStore note).
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// newUUID generates a new random UUID string for entities whose ID isn't
// already set by the caller before a Create* call.
func newUUID() string {
	return uuid.NewString()
}

// prefixCols rewrites a comma-separated (optionally multi-line/tab-indented)
// column list to prefix every column with "alias.", for reuse in SELECTs
// that join multiple tables sharing column names.
func prefixCols(alias, cols string) string {
	parts := strings.Split(cols, ",")
	out := make([]string, 0, len(parts))
	for _, c := range parts {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		out = append(out, alias+"."+c)
	}
	return strings.Join(out, ", ")
}

// ConfigChangeChannel is the pg_notify channel CRUD writes publish on and
// ListenConfigChanges/HA watchers subscribe to (docs/AMENDMENTS.md A13,
// docs/CONTRACTS.md Part 7 "Propagation").
const ConfigChangeChannel = "gdhcp_config"

// defaultOptionCacheTTL matches config.DatabaseConfig.OptionCacheTTL's
// default (server/internal/config/defaults.go) for callers that construct a
// Postgres store without an explicit WithOptionCacheTTL option.
const defaultOptionCacheTTL = 60 * time.Second

// globalScopeEntityID is the fixed sentinel scope_entity_id used for
// dhcp_option_values rows at scope_level='global'. The table's
// UNIQUE(option_code, scope_level, scope_entity_id) constraint needs a
// concrete, consistent entity id for the level that has no owning entity;
// every global write/read in this package uses this constant so there is
// ever only one global row per option code. See notes_for_integration.
const globalScopeEntityID = "00000000-0000-0000-0000-000000000000"

// Postgres is the PostgreSQL-backed Store implementation.
type Postgres struct {
	pool  *pgxpool.Pool
	cache *optionCache

	txnMu    sync.Mutex
	txnCount map[string]*dhcpTxnCounters
}

var _ Store = (*Postgres)(nil)

// StatsFlusher is satisfied by *Postgres. StatsRecorder's Record* methods
// buffer in memory to stay off the DHCP hot path (docs/CONTRACTS.md Part 1);
// the integration layer must call FlushStats periodically (e.g. every 10s
// alongside SampleUtilization's 60s cadence) to persist them into
// stats_dhcp_transactions. Exposed as a narrow interface, per amendment A10's
// spirit, rather than widening the Store interface itself.
type StatsFlusher interface {
	FlushStats(ctx context.Context) error
}

// Option configures a Postgres store at construction time.
type Option func(*Postgres)

// WithOptionCacheTTL overrides the default option-resolution cache TTL.
// Wiring code should pass config.DatabaseConfig.OptionCacheTTL here; the
// zero value falls back to defaultOptionCacheTTL. This is an additive,
// backward-compatible extension of the NewPostgres(pool) forward
// declaration in interface.go (amendment A13) — a call with no options
// still compiles and behaves per that declared signature.
func WithOptionCacheTTL(ttl time.Duration) Option {
	return func(p *Postgres) {
		if ttl > 0 {
			p.cache.ttl = ttl
		}
	}
}

// NewPostgres constructs a Store backed by PostgreSQL.
func NewPostgres(pool *pgxpool.Pool, opts ...Option) Store {
	p := &Postgres{
		pool:     pool,
		cache:    newOptionCache(defaultOptionCacheTTL),
		txnCount: make(map[string]*dhcpTxnCounters),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

const (
	defaultListLimit = 100
	maxListLimit     = 1000
)

// normalizeListParams applies docs/CONTRACTS.md Part 2's default/max limit
// rules ("Default: 100, Max: 1000").
func normalizeListParams(p ListParams) (limit, offset int) {
	limit = p.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	offset = p.Offset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// orderByClause builds a safe ORDER BY clause from a ListParams.Sort string
// (comma-separated fields, "-" prefix for DESC), restricted to allowed's
// keys (JSON/query field name -> actual SQL column) to prevent SQL
// injection through caller-controlled sort fields. fallback is used verbatim
// when sort is empty or every requested field is unknown.
func orderByClause(sort string, allowed map[string]string, fallback string) string {
	if sort == "" {
		return fallback
	}
	parts := strings.Split(sort, ",")
	clauses := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dir := "ASC"
		field := part
		if strings.HasPrefix(part, "-") {
			dir = "DESC"
			field = part[1:]
		}
		col, ok := allowed[field]
		if !ok {
			continue
		}
		clauses = append(clauses, col+" "+dir)
	}
	if len(clauses) == 0 {
		return fallback
	}
	return strings.Join(clauses, ", ")
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

// PostgreSQL SQLSTATE codes this package maps onto models sentinel errors.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateExclusionViolation  = "23P01"
	sqlStateForeignKeyViolation = "23503"
	sqlStateNotNullViolation    = "23502"
	sqlStateCheckViolation      = "23514"
)

// mapWriteError translates a pgx/pgconn error from an INSERT/UPDATE into the
// models sentinel errors the API layer maps to HTTP status codes
// (docs/CONTRACTS.md Part 6). op names the operation for the wrapped
// message (e.g. "create vrf"); entity names the entity for context.
func mapWriteError(err error, op string) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlStateUniqueViolation, sqlStateExclusionViolation:
			return fmt.Errorf("store: %s: %s: %w", op, pgErr.Message, models.ErrConflict)
		case sqlStateForeignKeyViolation, sqlStateNotNullViolation, sqlStateCheckViolation:
			return fmt.Errorf("store: %s: %s: %w", op, pgErr.Message, models.ErrInvalidInput)
		}
	}
	return fmt.Errorf("store: %s: %w", op, err)
}

// mapReadError translates a query error, collapsing pgx.ErrNoRows into
// models.ErrNotFound.
func mapReadError(err error, op string) error {
	return mapReadError2(err, models.ErrNotFound, op)
}

// mapReadError2 is mapReadError with a caller-chosen sentinel for the
// pgx.ErrNoRows case (e.g. models.ErrUnauthorized for an invalid API token,
// where "not found" would leak whether a token exists).
func mapReadError2(err error, notFoundSentinel error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("store: %s: %w", op, notFoundSentinel)
	}
	return fmt.Errorf("store: %s: %w", op, err)
}

// ---------------------------------------------------------------------------
// Small scan/bind helpers shared across postgres_*.go
// ---------------------------------------------------------------------------

// nilIfZeroIP returns nil when ip is the zero value, otherwise ip. Used so an
// unset net.IP struct field binds as SQL NULL rather than an empty address.
func nilIfZeroIP(ip net.IP) any {
	if len(ip) == 0 {
		return nil
	}
	return ip
}

// nilIfEmpty returns nil for an empty string, otherwise a pointer to it, so
// optional filter/ID arguments bind as SQL NULL rather than "".
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// notifyConfigChanged is a best-effort NOTIFY on ConfigChangeChannel, called
// from every CRUD write that affects DHCP-serving configuration (amendment
// A13's ClusterStore note: "call it from CRUD writes"). It never fails the
// caller's write — a missed notification only delays secondaries until their
// next poll (docs/CONTRACTS.md Part 7 fallback).
func (p *Postgres) notifyConfigChanged(ctx context.Context) {
	if err := p.NotifyConfigChange(ctx, ConfigChangeChannel); err != nil {
		slog.Default().Warn("store: config-change notify failed", "error", err)
	}
}

// ---------------------------------------------------------------------------
// ConfigChangeStore
// ---------------------------------------------------------------------------

// NotifyConfigChange sends a NOTIFY on channel with an RFC3339 timestamp
// payload so LISTENers can log latency; the payload is informational only.
func (p *Postgres) NotifyConfigChange(ctx context.Context, channel string) error {
	_, err := p.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: notify %s: %w", channel, err)
	}
	return nil
}

// ListenConfigChanges opens a dedicated connection (hijacked out of the pool
// so it is never recycled mid-LISTEN) and forwards NOTIFY payloads on
// channel until closeFn is called or the connection errors.
func (p *Postgres) ListenConfigChanges(ctx context.Context, channel string) (<-chan string, func() error, error) {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("store: acquire listen connection: %w", err)
	}
	raw := conn.Hijack()

	listenSQL := fmt.Sprintf("LISTEN %s", pgx.Identifier{channel}.Sanitize())
	if _, err := raw.Exec(ctx, listenSQL); err != nil {
		raw.Close(context.Background())
		return nil, nil, fmt.Errorf("store: listen %s: %w", channel, err)
	}

	listenCtx, cancel := context.WithCancel(context.Background())
	out := make(chan string)

	var closeOnce sync.Once
	closeFn := func() error {
		var cerr error
		closeOnce.Do(func() {
			cancel()
			cerr = raw.Close(context.Background())
		})
		return cerr
	}

	go func() {
		defer close(out)
		defer closeFn()
		for {
			n, err := raw.WaitForNotification(listenCtx)
			if err != nil {
				return
			}
			select {
			case out <- n.Payload:
			case <-listenCtx.Done():
				return
			}
		}
	}()

	return out, closeFn, nil
}
