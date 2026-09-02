package dashboard

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// subscriberBuffer is how many pending events one SSE connection may fall
// behind by before the hub starts dropping its messages. A subscriber this
// far behind is not reading its socket; blocking the shared poller on it
// would stall every other dashboard, so drop for the slow one instead --
// the same trade-off Events already makes for cluster pushes.
const subscriberBuffer = 16

// sseMessage is one named SSE event queued for a subscriber.
type sseMessage struct {
	event string
	data  any
}

// hubStore is the slice of store.Store the poller actually uses, declared
// here in the consumer rather than taking the full aggregate (amendment
// A10's decoupling rule). store.Store satisfies it structurally, so Events
// still passes its store straight through; the narrowing is what lets the
// hub be tested against a three-method fake instead of the whole interface.
type hubStore interface {
	GetDashboardStats(ctx context.Context, window time.Duration) (*models.DashboardStats, error)
	ListScopes(ctx context.Context, params store.ListParams) (store.ListResult[*models.DHCPScope], error)
	GetScopeUtilization(ctx context.Context, scopeID string) (*models.Utilization, error)
}

// eventHub polls the dashboard rollup and per-scope lease churn ONCE per
// interval and fans the result out to every open /api/v1/events stream.
//
// Each connection used to run its own ticker with its own churnTracker, so
// the query load scaled with the number of open dashboards rather than with
// the size of the deployment: ten tabs watching fifty scopes issued ten
// GetDashboardStats, ten ListScopes and five hundred GetScopeUtilization
// calls every ten seconds, all returning the same numbers. Now that cost is
// paid once no matter how many viewers there are.
//
// The poller is reference-counted rather than owned by server.Run: it starts
// when the first subscriber attaches and stops when the last one leaves, so
// a deployment with no dashboard open issues no queries at all, and there is
// no lifecycle to thread through api.New. Server shutdown closes the
// connections, which unsubscribes them, which stops the poller.
type eventHub struct {
	st  hubStore
	log *slog.Logger

	mu     sync.Mutex
	subs   map[int64]chan sseMessage
	nextID int64
	cancel context.CancelFunc // non-nil exactly while the poller runs

	// lastStats/lastChurn are the most recent poll's results, replayed to a
	// newly attached subscriber so a freshly opened dashboard renders at
	// once instead of waiting out a tick. lastChurn holds the FULL current
	// per-scope picture, not the delta, because a new subscriber has no
	// prior state for a delta to be meaningful against.
	lastStats *models.DashboardStats
	lastChurn map[string]scopeChurn
}

func newEventHub(st hubStore, log *slog.Logger) *eventHub {
	return &eventHub{
		st:        st,
		log:       log,
		subs:      make(map[int64]chan sseMessage),
		lastChurn: make(map[string]scopeChurn),
	}
}

// subscribe attaches a new stream. The returned channel carries events until
// the returned release func is called, which the caller must always do.
func (h *eventHub) subscribe() (<-chan sseMessage, func()) {
	h.mu.Lock()

	id := h.nextID
	h.nextID++
	ch := make(chan sseMessage, subscriberBuffer)
	h.subs[id] = ch

	// Replay the latest snapshot into this subscriber's buffer before
	// anyone else can write to it, so it is always the first thing the
	// connection sends.
	if h.lastStats != nil {
		ch <- sseMessage{event: "stats", data: h.lastStats}
	}
	if len(h.lastChurn) > 0 {
		all := make([]scopeChurn, 0, len(h.lastChurn))
		for _, c := range h.lastChurn {
			all = append(all, c)
		}
		ch <- sseMessage{event: "leases", data: all}
	}

	if h.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.cancel = cancel
		go h.run(ctx)
	}
	h.mu.Unlock()

	return ch, func() { h.release(id) }
}

// release detaches a stream, stopping the poller if it was the last one.
func (h *eventHub) release(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
	if len(h.subs) == 0 && h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
}

// run polls immediately (so the first subscriber does not wait a full
// interval) and then every statsInterval until ctx is cancelled.
func (h *eventHub) run(ctx context.Context) {
	h.poll(ctx)

	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.poll(ctx)
		}
	}
}

// poll runs one round of queries and broadcasts what changed.
func (h *eventHub) poll(ctx context.Context) {
	if s, err := h.st.GetDashboardStats(ctx, defaultStatsWindow); err != nil {
		h.log.Warn("api: sse stats query failed", "error", err)
	} else {
		h.mu.Lock()
		h.lastStats = s
		h.mu.Unlock()
		h.broadcast(sseMessage{event: "stats", data: s})
	}

	changed := h.pollChurn(ctx)
	if len(changed) > 0 {
		h.broadcast(sseMessage{event: "leases", data: changed})
	}
}

// pollChurn refreshes per-scope utilization and returns only the scopes
// whose active count moved, keeping the `leases` payload small on a quiet
// system. Scopes that disappeared are dropped from the tracked set.
func (h *eventHub) pollChurn(ctx context.Context) []scopeChurn {
	res, err := h.st.ListScopes(ctx, store.ListParams{Limit: churnListLimit})
	if err != nil {
		h.log.Warn("api: sse leases churn: list scopes failed", "error", err)
		return nil
	}

	current := make(map[string]scopeChurn, len(res.Items))
	for _, scope := range res.Items {
		u, err := h.st.GetScopeUtilization(ctx, scope.ID)
		if err != nil {
			continue
		}
		current[scope.ID] = scopeChurn{
			ScopeID: scope.ID,
			Active:  u.Active,
			Total:   u.Total,
			Pct:     u.UtilizationPercentage,
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	var changed []scopeChurn
	for id, c := range current {
		if prev, ok := h.lastChurn[id]; !ok || prev.Active != c.Active {
			changed = append(changed, c)
		}
	}
	h.lastChurn = current
	return changed
}

// broadcast queues msg for every attached subscriber, skipping any whose
// buffer is full.
func (h *eventHub) broadcast(msg sseMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- msg:
		default: // slow consumer: drop rather than stall the shared poller
		}
	}
}
