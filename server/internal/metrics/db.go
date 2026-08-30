package metrics

// SetDBPoolStats publishes a pgxpool.Stat snapshot. Callers poll
// pool.Stat() periodically (e.g. every 15s) and pass the three counts
// through rather than this package importing pgxpool directly, keeping
// metrics decoupled from the db package per amendment A10's spirit.
func (r *Registry) SetDBPoolStats(open, idle, inUse int32) {
	r.dbPoolOpenConns.Set(float64(open))
	r.dbPoolIdleConns.Set(float64(idle))
	r.dbPoolInUseConns.Set(float64(inUse))
}
