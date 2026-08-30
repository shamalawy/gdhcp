package ha

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// electionQueryTimeout bounds every election query independently of the
// caller's context, per this unit's task scope.
const electionQueryTimeout = 500 * time.Millisecond

// runElection performs one election cycle: query the elected primary,
// resolve a bootstrap winner if nobody has a fresh heartbeat yet, and apply
// the result to this node's state machine. Called once at startup (when HA
// is enabled) and then on every election_interval_ms tick.
func (m *Manager) runElection(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, electionQueryTimeout)
	defer cancel()

	start := time.Now()
	winner, err := m.store.ElectionQuery(cctx, m.clusterID)
	m.metricsRec.electionQueryDuration(m.clusterID, time.Since(start))

	if err != nil && !errors.Is(err, models.ErrNotFound) {
		m.log.Warn("ha: election query failed", "cluster_id", m.clusterID, "error", err)
		m.recordDBFailure(ctx, err)
		return
	}

	if m.recordDBSuccess(ctx) {
		// Just rejoined as SECONDARY after a fencing episode; re-evaluate
		// the election result on the next tick rather than potentially
		// promoting ourselves in the same cycle we recovered in.
		return
	}

	if errors.Is(err, models.ErrNotFound) {
		winner = ""
	}
	if winner == "" {
		winner = m.bootstrapElect(ctx)
		if winner == "" {
			return // no registered servers yet, or DB unavailable; retry next tick
		}
	}

	m.applyElectionResult(ctx, winner)
}

// bootstrapElect resolves a winner locally when the election query finds no
// server with a fresh heartbeat: either a genuinely new cluster (nobody has
// ever written a heartbeat) or a full outage where every heartbeat has
// expired. docs/design/ha-cluster.md's election query only ever considers
// rows that already have a fresh heartbeat — with zero such rows nobody
// would ever be elected and the cluster would deadlock forever, since only
// a PRIMARY writes heartbeats and nobody can become PRIMARY without one.
// This resolves it the same way the design doc's own "Reset cluster"
// runbook does: pick the lowest node_id among all *registered* servers
// (tie-break: earliest created_at, matching the election query's own
// ORDER BY) and let it self-promote. That server's first heartbeat write
// then makes the normal election query authoritative again on the very
// next tick.
func (m *Manager) bootstrapElect(ctx context.Context) string {
	servers, err := m.store.ListServers(ctx, m.clusterID)
	if err != nil {
		m.log.Warn("ha: bootstrap election: failed to list servers", "cluster_id", m.clusterID, "error", err)
		m.recordDBFailure(ctx, err)
		return ""
	}
	if len(servers) == 0 {
		return ""
	}
	sort.Slice(servers, func(i, j int) bool {
		if servers[i].NodeID != servers[j].NodeID {
			return servers[i].NodeID < servers[j].NodeID
		}
		return servers[i].CreatedAt.Before(servers[j].CreatedAt)
	})
	return servers[0].ID
}

// applyElectionResult transitions this node's role to match winner, unless
// a manual Failover cool-down (see failover.go) is suppressing
// self-re-election.
func (m *Manager) applyElectionResult(ctx context.Context, winner string) {
	m.mu.RLock()
	coolingDown := m.steppedDownUntil.After(time.Now())
	m.mu.RUnlock()

	if winner == m.serverID && coolingDown {
		winner = ""
	}

	if winner == m.serverID {
		if m.sm.Current() != StatePrimary {
			m.transitionTo(ctx, StatePrimary, "election_won")
		}
		return
	}
	if m.sm.Current() != StateSecondary {
		m.transitionTo(ctx, StateSecondary, "election_lost")
	}
}
