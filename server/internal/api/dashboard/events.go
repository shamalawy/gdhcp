package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// ClusterWatcher is the slice of api.HAStatus (amendment A10) the SSE stream
// needs to push `cluster` events. Declared locally (rather than imported)
// so this package never depends on its parent package api — Go's structural
// typing lets router.go pass its concrete api.HAStatus value here directly.
type ClusterWatcher interface {
	Subscribe(fn func(clusterID string, health *models.ClusterHealth)) (unsubscribe func())
}

const (
	statsInterval     = 10 * time.Second
	heartbeatInterval = 15 * time.Second
	churnListLimit    = 1000
)

type clusterPush struct {
	ClusterID string
	Health    *models.ClusterHealth
}

// Events handles GET /api/v1/events: an amendment-A12 Server-Sent Events
// stream emitting `stats` (dashboard rollup, ~every 10s), `leases` (per-scope
// active-lease-count churn, polled alongside stats — the store surface has
// no lease-change subscription, so polling is the v1 fallback A12 explicitly
// allows), and `cluster` (pushed immediately on every HAStatus.Subscribe
// callback). A ": heartbeat" comment every 15s keeps intermediary proxies
// from closing the idle connection.
func Events(st store.Store, cluster ClusterWatcher, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// http.NewResponseController, not a raw `w.(http.Flusher)` type
		// assertion: logger.Middleware (mounted outermost, ahead of every
		// handler) wraps w in statusRecorder, which doesn't itself
		// implement http.Flusher, so a direct assertion here always fails
		// even though the underlying connection flushes just fine.
		// ResponseController walks statusRecorder's Unwrap() method (added
		// for exactly this) to reach the real Flusher underneath.
		rc := http.NewResponseController(w)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx response buffering for SSE
		w.WriteHeader(http.StatusOK)
		if err := rc.Flush(); err != nil {
			// Headers are already committed at this point, so there's no
			// clean error response left to send — just log and give up.
			log.Error("api: response writer does not support flushing; SSE unavailable", "error", err)
			return
		}

		ctx := r.Context()

		pushes := make(chan clusterPush, 8)
		var unsubscribe func()
		if cluster != nil {
			unsubscribe = cluster.Subscribe(func(clusterID string, health *models.ClusterHealth) {
				select {
				case pushes <- clusterPush{ClusterID: clusterID, Health: health}:
				default: // slow/gone consumer: drop rather than block the HA manager
				}
			})
			defer unsubscribe()
		}

		statsTicker := time.NewTicker(statsInterval)
		defer statsTicker.Stop()
		heartbeatTicker := time.NewTicker(heartbeatInterval)
		defer heartbeatTicker.Stop()

		churn := newChurnTracker(st, log)

		// Send an initial snapshot immediately so the UI doesn't wait a full
		// tick before showing anything.
		emitStats(ctx, w, rc, st, log)
		churn.emit(ctx, w, rc)

		for {
			select {
			case <-ctx.Done():
				return
			case <-statsTicker.C:
				emitStats(ctx, w, rc, st, log)
				churn.emit(ctx, w, rc)
			case <-heartbeatTicker.C:
				if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
					return
				}
				_ = rc.Flush()
			case p := <-pushes:
				// Send the bare ClusterHealth (models.ClusterHealth's own
				// json tags are already the snake_case shape
				// SSEClusterEvent.data expects on the frontend) rather than
				// the clusterPush wrapper — wrapping it would serialize as
				// untagged, PascalCase {ClusterID,Health} instead.
				writeEvent(w, rc, "cluster", p.Health)
			}
		}
	}
}

func emitStats(ctx context.Context, w http.ResponseWriter, rc *http.ResponseController, st store.Store, log *slog.Logger) {
	s, err := st.GetDashboardStats(ctx, defaultStatsWindow)
	if err != nil {
		log.Warn("api: sse stats query failed", "error", err)
		return
	}
	writeEvent(w, rc, "stats", s)
}

func writeEvent(w http.ResponseWriter, rc *http.ResponseController, event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return
	}
	_ = rc.Flush()
}

// churnTracker polls per-scope active-lease counts and emits only the scopes
// whose active count changed since the last poll, keeping the `leases`
// event payload small on a quiet system.
type churnTracker struct {
	st   store.Store
	log  *slog.Logger
	last map[string]int64
}

func newChurnTracker(st store.Store, log *slog.Logger) *churnTracker {
	return &churnTracker{st: st, log: log, last: make(map[string]int64)}
}

type scopeChurn struct {
	ScopeID string  `json:"scope_id"`
	Active  int64   `json:"active"`
	Total   int64   `json:"total"`
	Pct     float64 `json:"pct"`
}

func (c *churnTracker) emit(ctx context.Context, w http.ResponseWriter, rc *http.ResponseController) {
	res, err := c.st.ListScopes(ctx, store.ListParams{Limit: churnListLimit})
	if err != nil {
		c.log.Warn("api: sse leases churn: list scopes failed", "error", err)
		return
	}

	var changed []scopeChurn
	seen := make(map[string]bool, len(res.Items))
	for _, scope := range res.Items {
		seen[scope.ID] = true
		u, err := c.st.GetScopeUtilization(ctx, scope.ID)
		if err != nil {
			continue
		}
		if prev, ok := c.last[scope.ID]; !ok || prev != u.Active {
			changed = append(changed, scopeChurn{ScopeID: scope.ID, Active: u.Active, Total: u.Total, Pct: u.UtilizationPercentage})
		}
		c.last[scope.ID] = u.Active
	}
	for id := range c.last {
		if !seen[id] {
			delete(c.last, id)
		}
	}

	if len(changed) > 0 {
		writeEvent(w, rc, "leases", changed)
	}
}
