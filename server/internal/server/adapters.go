package server

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/ha"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

// haStatusAdapter adapts *ha.Manager to the api package's consumer-side
// api.HAStatus interface (docs/AMENDMENTS.md A10). ha.Manager's own method
// set doesn't line up signature-for-signature with api.HAStatus (Failover
// takes no clusterID, Health takes no clusterID, ShouldServe vs. Serving,
// Subscribe with no callback), so this integration-layer type bridges the
// two rather than either package depending on the other directly.
type haStatusAdapter struct {
	m *ha.Manager
}

func newHAStatusAdapter(m *ha.Manager) *haStatusAdapter { return &haStatusAdapter{m: m} }

// Failover implements api.HAStatus.Failover for POST
// /api/v1/clusters/{id}/failover. ha.Manager only ever manages the single
// cluster this node belongs to, so a clusterID naming a different cluster is
// rejected rather than silently failing over the wrong cluster; an empty
// clusterID is accepted as shorthand for "this node's cluster". Per
// ha/failover.go's doc comment, targetServerID is advisory only (the
// election algorithm has no "elect this specific node" primitive) -- the
// returned "new primary" ID reflects the requested target when one was
// given, since the actual election outcome isn't known synchronously at the
// point Failover returns.
func (a *haStatusAdapter) Failover(ctx context.Context, clusterID, targetServerID string) (string, error) {
	if clusterID != "" && clusterID != a.m.ClusterID() {
		return "", fmt.Errorf("server: cluster %q is not this node's cluster (%q); manual failover must be requested against a server belonging to the target cluster", clusterID, a.m.ClusterID())
	}
	if err := a.m.Failover(ctx, targetServerID); err != nil {
		return "", err
	}
	return targetServerID, nil
}

// ClusterHealth implements api.HAStatus.ClusterHealth, delegating to
// ha.Manager.Health for this node's own cluster.
func (a *haStatusAdapter) ClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error) {
	if clusterID != "" && clusterID != a.m.ClusterID() {
		return nil, fmt.Errorf("server: cluster %q is not this node's cluster (%q)", clusterID, a.m.ClusterID())
	}
	return a.m.Health(ctx)
}

// Serving implements api.HAStatus.Serving.
func (a *haStatusAdapter) Serving() bool { return a.m.ShouldServe() }

// Subscribe implements api.HAStatus.Subscribe, driving the SSE `cluster`
// event (amendment A12). ha.Manager.Subscribe() only signals "this node
// observed a cluster config change" (config_watcher.go), not role
// transitions specifically -- it's the only change-notification primitive
// the ha package exposes, and re-fetching Health() on every signal still
// surfaces role/heartbeat changes promptly enough for the dashboard. See
// notes_for_integration.
func (a *haStatusAdapter) Subscribe(fn func(clusterID string, health *models.ClusterHealth)) (unsubscribe func()) {
	ch := a.m.Subscribe()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				if h, err := a.m.Health(context.Background()); err == nil {
					fn(a.m.ClusterID(), h)
				}
			}
		}
	}()
	return func() { close(done) }
}

// engineInfoAdapter adapts *dhcp.Engine to the api package's consumer-side
// api.EngineInfo interface. The dhcp package exposes no Ready/Uptime/Stats
// methods of its own (only Start/Stop/UseMetrics/SetPingCheck/
// SetExpiryReaperInterval), so this integration-layer wrapper tracks
// readiness and start time itself around the Start() call Run makes.
type engineInfoAdapter struct {
	interfaces int
	ready      atomic.Bool
	startedAt  time.Time
}

func newEngineInfoAdapter(interfaceCount int) *engineInfoAdapter {
	return &engineInfoAdapter{interfaces: interfaceCount}
}

// markStarted records that dhcp.Engine.Start succeeded. Run only
// constructs an engine (and this adapter) at all when
// len(cfg.DHCP.Interfaces) > 0 (amendment A11's API/UI-only mode passes a
// nil api.EngineInfo instead), so Ready() never has to represent "true with
// nothing actually listening".
func (a *engineInfoAdapter) markStarted() {
	a.startedAt = time.Now()
	a.ready.Store(true)
}

func (a *engineInfoAdapter) Ready() bool { return a.ready.Load() }

func (a *engineInfoAdapter) Uptime() time.Duration {
	if !a.ready.Load() {
		return 0
	}
	return time.Since(a.startedAt)
}

func (a *engineInfoAdapter) Stats() map[string]any {
	return map[string]any{
		"ready":          a.ready.Load(),
		"interfaces":     a.interfaces,
		"uptime_seconds": a.Uptime().Seconds(),
	}
}

// optionCacheInvalidator is satisfied by *store.Postgres (cache.go), used
// here as a local structural interface so this package doesn't need a named
// export from store for a single narrow method (mirrors store.StatsFlusher's
// own rationale for staying outside the Store aggregate).
type optionCacheInvalidator interface {
	InvalidateOptionCache()
}
