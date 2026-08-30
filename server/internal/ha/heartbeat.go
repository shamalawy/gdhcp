package ha

import (
	"context"
	"time"
)

// tickHeartbeat writes this node's heartbeat if (and only if) it currently
// believes itself to be PRIMARY. docs/design/ha-cluster.md: "Secondary
// server: does not write heartbeats; it only reads them" — only the
// election result, never the heartbeat loop, ever assigns this node the
// PRIMARY role.
func (m *Manager) tickHeartbeat(ctx context.Context) {
	if m.sm.Current() != StatePrimary {
		return
	}
	m.writeHeartbeat(ctx)
}

// writeHeartbeat upserts this node's heartbeat row (heartbeat_time = now(),
// role = 'PRIMARY' per store.ClusterStore.WriteHeartbeat's contract) and
// records the DB outcome for fencing purposes. Called from the heartbeat
// ticker, and once immediately on promotion (onEnterPrimary) so a new
// primary's heartbeat is visible to peers without waiting a full
// heartbeat_interval_ms.
func (m *Manager) writeHeartbeat(ctx context.Context) {
	start := time.Now()
	err := m.store.WriteHeartbeat(ctx, m.clusterID, m.serverID)
	m.metricsRec.heartbeatWriteDuration(m.serverID, time.Since(start))

	if err != nil {
		m.log.Warn("ha: heartbeat write failed", "cluster_id", m.clusterID, "server_id", m.serverID, "error", err)
		m.recordDBFailure(ctx, err)
		return
	}
	m.recordDBSuccess(ctx)
}
