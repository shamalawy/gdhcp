package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Healthy pings pool with a bounded timeout, returning a wrapped error
// suitable for surfacing as models.ErrServiceUnavailable at the API layer
// (GET /api/v1/health per docs/api/endpoints.md).
func Healthy(ctx context.Context, pool *pgxpool.Pool) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		return fmt.Errorf("db: unhealthy: %w", err)
	}
	return nil
}
