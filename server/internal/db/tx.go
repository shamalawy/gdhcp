package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTx begins a transaction with the default (read committed) isolation
// level, runs fn, and commits if fn returns nil or rolls back otherwise
// (including on panic, which is re-panicked after rollback). Per amendment
// A6, READ COMMITTED is sufficient for lease allocation because atomicity
// comes from the pool's UNIQUE(pool_id, ip_address) constraint, not from
// isolation level.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	return WithTxOptions(ctx, pool, pgx.TxOptions{}, fn)
}

// WithSerializableTx begins a SERIALIZABLE transaction. Use for operations
// that must not observe or produce write skew across concurrent servers,
// per docs/CONTRACTS.md Part 7's isolation guidance for cases stricter than
// the A6 allocation path (e.g. election/config-version bookkeeping).
func WithSerializableTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	return WithTxOptions(ctx, pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, fn)
}

// WithTxOptions begins a transaction with the given options, runs fn, and
// commits/rolls back as WithTx does.
func WithTxOptions(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = fmt.Errorf("%w (rollback also failed: %v)", err, rbErr)
			}
			return
		}
		if cErr := tx.Commit(ctx); cErr != nil {
			err = fmt.Errorf("db: commit tx: %w", cErr)
		}
	}()

	err = fn(tx)
	return err
}

// IsSerializationFailure reports whether err is a PostgreSQL serialization
// failure (SQLSTATE 40001) or deadlock (40P01), the two errors a
// SERIALIZABLE-transaction retry loop should catch and retry on.
func IsSerializationFailure(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		switch pgErr.SQLState() {
		case "40001", "40P01":
			return true
		}
	}
	return false
}
