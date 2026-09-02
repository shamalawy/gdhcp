package dashboard

import (
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

type scopeChurn struct {
	ScopeID string  `json:"scope_id"`
	Active  int64   `json:"active"`
	Total   int64   `json:"total"`
	Pct     float64 `json:"pct"`
}

// Events handles GET /api/v1/events: an amendment-A12 Server-Sent Events
// stream emitting `stats` (dashboard rollup, ~every 10s), `leases` (per-scope
// active-lease-count churn, polled alongside stats — the store surface has
// no lease-change subscription, so polling is the v1 fallback A12 explicitly
// allows), and `cluster` (pushed immediately on every HAStatus.Subscribe
// callback). A ": heartbeat" comment every 15s keeps intermediary proxies
// from closing the idle connection.
//
// The polling happens once for the whole server, in the eventHub created
// here at wiring time (hub.go); this handler only forwards what the hub
// broadcasts. Every connection used to poll independently, so query load
// scaled with the number of open dashboards instead of with the size of the
// deployment.
func Events(st store.Store, cluster ClusterWatcher, log *slog.Logger) http.HandlerFunc {
	hub := newEventHub(st, log)

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
		if cluster != nil {
			unsubscribe := cluster.Subscribe(func(clusterID string, health *models.ClusterHealth) {
				select {
				case pushes <- clusterPush{ClusterID: clusterID, Health: health}:
				default: // slow/gone consumer: drop rather than block the HA manager
				}
			})
			defer unsubscribe()
		}

		// Attaching primes the channel with the hub's latest snapshot, so
		// the UI renders immediately rather than waiting out a tick.
		events, release := hub.subscribe()
		defer release()

		heartbeatTicker := time.NewTicker(heartbeatInterval)
		defer heartbeatTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-events:
				if !ok {
					return
				}
				writeEvent(w, rc, msg.event, msg.data)
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
