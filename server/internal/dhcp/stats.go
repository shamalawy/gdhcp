package dhcp

// recordDiscover/recordOffer/.../recordNoLeases fan a per-transaction
// counter out to both store.StatsRecorder (which feeds the dashboard's
// dhcp_stats rollups and SSE `stats` push, amendment A12) and the
// Prometheus registry (docs/CONTRACTS.md Part 8's gdhcp_dhcp_*_total
// metrics), when one has been wired in via UseMetrics. Keeping this as one
// call site per event, rather than inlining both calls at every DORA
// handler call site, is what makes UseMetrics's optionality (see engine.go)
// safe: every call site here is nil-checked once.

func (e *Engine) recordDiscover(scopeID string) {
	e.store.RecordDiscover(scopeID)
	if e.metricsReg != nil {
		e.metricsReg.DHCPDiscover(scopeID)
	}
}

func (e *Engine) recordOffer(scopeID string) {
	e.store.RecordOffer(scopeID)
	if e.metricsReg != nil {
		e.metricsReg.DHCPOffer(scopeID)
	}
}

func (e *Engine) recordRequest(scopeID string) {
	e.store.RecordRequest(scopeID)
	if e.metricsReg != nil {
		e.metricsReg.DHCPRequest(scopeID)
	}
}

func (e *Engine) recordAck(scopeID string) {
	e.store.RecordAck(scopeID)
	if e.metricsReg != nil {
		e.metricsReg.DHCPAck(scopeID)
	}
}

func (e *Engine) recordNak(scopeID string) {
	e.store.RecordNak(scopeID)
	if e.metricsReg != nil {
		e.metricsReg.DHCPNak(scopeID)
	}
}

func (e *Engine) recordDecline(scopeID string) {
	e.store.RecordDecline(scopeID)
	if e.metricsReg != nil {
		e.metricsReg.DHCPDecline(scopeID)
	}
}

func (e *Engine) recordRelease(scopeID string) {
	e.store.RecordRelease(scopeID)
	if e.metricsReg != nil {
		e.metricsReg.DHCPRelease(scopeID)
	}
}

func (e *Engine) recordNoLeases(scopeID string) {
	e.store.RecordNoLeases(scopeID)
	if e.metricsReg != nil {
		e.metricsReg.DHCPNoLeases(scopeID)
	}
}
