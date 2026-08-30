package api

import (
	"context"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// HAStatus is the API layer's consumer-side view of the HA cluster manager
// (docs/AMENDMENTS.md A10: the API package must not import server/internal/ha
// directly since it is built in parallel — the integration layer wires a
// concrete implementation into router.New). It covers what the REST surface
// needs beyond plain DB reads (which go through store.ClusterStore
// directly): live in-process health, the manual-failover action, and this
// node's current serving eligibility.
type HAStatus interface {
	// Failover triggers a manual failover for clusterID
	// (POST /api/v1/clusters/{id}/failover). targetServerID selects the new
	// primary explicitly; an empty string lets the HA manager pick per its
	// normal election algorithm. Returns the server ID that became primary.
	Failover(ctx context.Context, clusterID, targetServerID string) (newPrimaryServerID string, err error)

	// ClusterHealth returns this node's live in-process view of clusterID's
	// health (role, VIP ownership, heartbeat ages) — used for the SSE
	// `cluster` event so pushes don't require a DB round trip. Handlers that
	// only need an on-demand snapshot (GET /clusters/{id}/health) read
	// through store.ClusterStore.GetClusterHealth instead.
	ClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error)

	// Serving reports whether this node currently believes it should answer
	// DHCP/serve as primary (true in standalone mode per amendment A11, or
	// when this node holds PRIMARY in an HA cluster). Surfaced on
	// GET /api/v1/health.
	Serving() bool

	// Subscribe registers fn to be invoked whenever any cluster's role or
	// heartbeat health changes, driving the SSE `cluster` event (amendment
	// A12). The returned function cancels the subscription.
	Subscribe(fn func(clusterID string, health *models.ClusterHealth)) (unsubscribe func())
}

// EngineInfo is the API layer's consumer-side view of the DHCP engine
// (docs/AMENDMENTS.md A10: no direct import of server/internal/dhcp). It
// supplies engine-level stats and readiness for GET /api/v1/health and the
// dashboard, without the API needing to know anything about packet
// processing internals.
type EngineInfo interface {
	// Ready reports whether the DHCP engine finished startup and is actively
	// listening. False when cfg.dhcp.interfaces is empty (amendment A11's
	// API/UI-only mode) or before the engine has completed startup.
	Ready() bool

	// Uptime returns how long the engine has been running; zero if not
	// started.
	Uptime() time.Duration

	// Stats returns a snapshot of engine-level counters (e.g. interfaces
	// bound, packets processed) for informational display. Kept as a generic
	// map since these are engine-internal and engine-owned; the DHCP
	// protocol-level per-scope transaction counters used by
	// GET /api/v1/scopes/{id}/stats and the dashboard come from
	// store.StatsRecorder instead.
	Stats() map[string]any
}
