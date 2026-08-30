package ha

import (
	"context"
	"fmt"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// Health returns this cluster's health rollup, shaped exactly like
// docs/api/endpoints.md's ClusterHealth example: it delegates the
// per-server roll-up (servers, heartbeat ages, overall cluster_state) to
// store.ClusterStore.GetClusterHealth — which alone has visibility into
// every server's heartbeat row — and fills in local_server_id/local_role
// from this node's own state machine, since GetClusterHealth has no way to
// know which of the servers it lists is "local" to the caller.
func (m *Manager) Health(ctx context.Context) (*models.ClusterHealth, error) {
	h, err := m.store.GetClusterHealth(ctx, m.clusterID)
	if err != nil {
		return nil, fmt.Errorf("ha: get cluster health: %w", err)
	}
	h.LocalServerID = m.serverID
	h.LocalRole = m.Role()
	return h, nil
}

// Role reports this node's current HA role for API/UI display.
// models.ClusterRole only has PRIMARY/SECONDARY values, so INITIALIZING and
// ERROR — states in which this node never holds the VIP or claims to be
// authoritative — are reported as SECONDARY, the safe default.
func (m *Manager) Role() models.ClusterRole {
	if m.sm.Current() == StatePrimary {
		return models.ClusterRolePrimary
	}
	return models.ClusterRoleSecondary
}

// State returns the raw HA state machine state (INITIALIZING, SECONDARY,
// PRIMARY, ERROR, or SHUTDOWN) — finer-grained than Role, mainly useful for
// logging/diagnostics and tests.
func (m *Manager) State() State {
	return m.sm.Current()
}

// ShouldServe reports whether this node should answer DHCP traffic right
// now. It is the method the DHCP engine's dhcp.ServingGate consumer-side
// interface (amendment A10) is expected to be satisfied by structurally.
//
// Per amendment A11, standalone mode (ha.enabled: false) always returns
// true: there is no election/fencing to speak of, and this node is the only
// one that could possibly serve. In clustered mode, both PRIMARY and
// SECONDARY serve concurrently (lease allocation is atomic in PostgreSQL —
// docs/design/ha-cluster.md's "Concurrent DHCP Serving"); only
// INITIALIZING (no election result yet), ERROR (fenced off from the
// database), and SHUTDOWN go dark.
func (m *Manager) ShouldServe() bool {
	if !m.cfg.HA.Enabled {
		return true
	}
	switch m.sm.Current() {
	case StatePrimary, StateSecondary:
		return true
	default:
		return false
	}
}

// ClusterID returns the resolved clusters.id row this node belongs to
// (valid only after Start returns successfully).
func (m *Manager) ClusterID() string { return m.clusterID }

// LocalServerID returns the resolved cluster_servers.id row for this node
// (valid only after Start returns successfully).
func (m *Manager) LocalServerID() string { return m.serverID }
