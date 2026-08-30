package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolStatsReporter is the minimal surface StartPoolStatsReporter needs from
// server/internal/metrics.Registry, declared here (rather than importing
// metrics directly) to keep db decoupled per amendment A10's spirit — the
// integration layer passes its concrete *metrics.Registry, which already
// satisfies this interface via its SetDBPoolStats method.
type PoolStatsReporter interface {
	SetDBPoolStats(open, idle, inUse int32)
}

// StartPoolStatsReporter polls pool.Stat() every interval and publishes the
// open/idle/in-use connection counts to reporter, until ctx is canceled. Run
// it in its own goroutine from server/app.go.
func StartPoolStatsReporter(ctx context.Context, pool *pgxpool.Pool, reporter PoolStatsReporter, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stat := pool.Stat()
			inUse := stat.AcquiredConns()
			idle := stat.IdleConns()
			reporter.SetDBPoolStats(inUse+idle, idle, inUse)
		}
	}
}
