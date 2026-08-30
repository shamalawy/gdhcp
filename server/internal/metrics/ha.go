package metrics

import "time"

// HeartbeatWriteDuration records how long a heartbeat write took for serverID.
func (r *Registry) HeartbeatWriteDuration(serverID string, d time.Duration) {
	r.heartbeatWriteDuration.WithLabelValues(serverID).Observe(msFloat(d))
}

// ElectionQueryDuration records how long an election query took for clusterID.
func (r *Registry) ElectionQueryDuration(clusterID string, d time.Duration) {
	r.electionQueryDuration.WithLabelValues(clusterID).Observe(msFloat(d))
}

// VIPAssignmentFailure increments the netlink VIP-assignment failure counter
// for clusterID.
func (r *Registry) VIPAssignmentFailure(clusterID string) {
	r.vipAssignmentFailures.WithLabelValues(clusterID).Inc()
}

// SetVIPAssigned publishes whether this node currently holds clusterID's VIP.
func (r *Registry) SetVIPAssigned(clusterID string, assigned bool) {
	v := 0.0
	if assigned {
		v = 1.0
	}
	r.vipAssigned.WithLabelValues(clusterID).Set(v)
}

// DBPartitionDetected increments the counter for clusterID when this node
// determines it can no longer reach the database (HA state machine's ERROR
// transition).
func (r *Registry) DBPartitionDetected(clusterID string) {
	r.dbPartitionDetected.WithLabelValues(clusterID).Inc()
}

func msFloat(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
