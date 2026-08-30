package ha

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Failover implements POST /api/v1/clusters/{id}/failover's manual
// failover action (docs/api/endpoints.md): it demotes this node from
// PRIMARY to SECONDARY, dropping the VIP immediately, so the next election
// cycle promotes whichever registered server has the lowest node_id among
// those still heartbeating.
//
// targetServerID is validated (best-effort, against ListServers) when
// non-empty but is otherwise advisory only: docs/design/ha-cluster.md's
// election algorithm has no notion of "elect this specific server" — only
// "elect the lowest node_id with a fresh heartbeat" — so a directed
// failover to an arbitrary target is not something this design supports
// beyond self-demotion. See notes_for_integration.
//
// Because ClusterStore.WriteHeartbeat only ever sets role='PRIMARY' (there
// is no method to clear/downgrade this node's own heartbeat row on
// demand — see notes_for_integration), simply stopping heartbeat writes
// would leave this node's last-written row looking fresh (and therefore
// still electable) for up to heartbeat_ttl_ms. Failover instead sets a
// local cool-down (steppedDownUntil): applyElectionResult refuses to
// re-promote this node to PRIMARY even if the election query still names
// it, until either the cool-down elapses or this node's heartbeat row
// naturally goes stale — whichever is later.
func (m *Manager) Failover(ctx context.Context, targetServerID string) error {
	if !m.cfg.HA.Enabled {
		return errors.New("ha: failover has no effect in standalone mode (ha.enabled: false)")
	}
	if m.sm.Current() != StatePrimary {
		return fmt.Errorf("ha: this server is not PRIMARY (current state %s); failover must be requested against the current primary", m.sm.Current())
	}

	if targetServerID != "" && targetServerID != m.serverID {
		if servers, err := m.store.ListServers(ctx, m.clusterID); err == nil {
			found := false
			for _, s := range servers {
				if s.ID == targetServerID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("ha: target server %q is not registered in cluster %q", targetServerID, m.clusterID)
			}
		}
		// A ListServers error here isn't fatal to the failover itself —
		// proceed with the demotion even if the target couldn't be
		// validated.
	}

	m.mu.Lock()
	m.steppedDownUntil = time.Now().Add(time.Duration(m.cfg.HA.HeartbeatTTLMs) * time.Millisecond)
	m.mu.Unlock()

	m.log.Info("ha: manual failover requested",
		"cluster_id", m.clusterID, "from_server", m.serverID, "requested_target", targetServerID)
	m.transitionTo(ctx, StateSecondary, "manual_failover")
	return nil
}
