package ha

import (
	"time"

	"github.com/shamalawy/gdhcp/server/internal/metrics"
)

// SetMetrics wires this Manager to the process-wide Prometheus registry.
// Call it after New and before Start.
//
// New's signature is fixed by this unit's task scope
// (New(cfg, st, log) (*Manager, error)) and doesn't take a *metrics.Registry
// — the integration agent owns constructing the single shared Registry
// (server/internal/server/app.go) and handing it to every subsystem, which
// this setter — rather than a constructor parameter — makes possible
// without coupling New to the order subsystems get built in. Every call
// site below is nil-safe, so a Manager that never has SetMetrics called on
// it (every unit test in this package, and any future standalone use)
// simply records no metrics rather than panicking.
func (m *Manager) SetMetrics(r *metrics.Registry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metricsReg = r
}

// metricsRecorder adapts the *metrics.Registry methods this package calls
// (docs/CONTRACTS.md Part 8's HA metrics: heartbeat_write_duration_ms,
// election_query_duration_ms, vip_assignment_failures_total, vip_assigned,
// db_partition_detected_total) into nil-safe no-ops, so call sites never
// need their own nil check.
type metricsRecorder struct{ m *Manager }

func (r metricsRecorder) reg() *metrics.Registry {
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	return r.m.metricsReg
}

func (r metricsRecorder) heartbeatWriteDuration(serverID string, d time.Duration) {
	if reg := r.reg(); reg != nil {
		reg.HeartbeatWriteDuration(serverID, d)
	}
}

func (r metricsRecorder) electionQueryDuration(clusterID string, d time.Duration) {
	if reg := r.reg(); reg != nil {
		reg.ElectionQueryDuration(clusterID, d)
	}
}

func (r metricsRecorder) vipAssignmentFailure(clusterID string) {
	if reg := r.reg(); reg != nil {
		reg.VIPAssignmentFailure(clusterID)
	}
}

func (r metricsRecorder) setVIPAssigned(clusterID string, assigned bool) {
	if reg := r.reg(); reg != nil {
		reg.SetVIPAssigned(clusterID, assigned)
	}
}

func (r metricsRecorder) dbPartitionDetected(clusterID string) {
	if reg := r.reg(); reg != nil {
		reg.DBPartitionDetected(clusterID)
	}
}
