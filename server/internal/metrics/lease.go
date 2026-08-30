package metrics

// AllocationRetries records how many candidate-IP attempts (amendment A6's
// retry loop, 0-3) it took to allocate a lease in scopeID.
func (r *Registry) AllocationRetries(scopeID string, retries int) {
	r.allocationRetries.WithLabelValues(scopeID).Observe(float64(retries))
}

// LeaseAllocationConflict increments the ON CONFLICT DO NOTHING race-loss
// counter for poolID.
func (r *Registry) LeaseAllocationConflict(poolID string) {
	r.leaseConflicts.WithLabelValues(poolID).Inc()
}
