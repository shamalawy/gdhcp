package dhcp

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Lease timing fractions per docs/CONTRACTS.md Part 3 / task spec: T1
// (renewal) defaults to 50% of the lease, dropping to 40% once the HA
// cluster has 2 or more registered servers (so a client's renewal attempt
// lands well before T2/rebinding even if its primary is briefly down); T2
// (rebinding) is always 87.5%.
const (
	t1FractionStandalone = 0.50
	t1FractionHA         = 0.40
	t2Fraction           = 0.875
)

// clusterSizeCacheTTL bounds how often lease.go re-queries ListServers for
// the T1 fraction decision — this runs on every lease grant, so it must not
// add a database round-trip to the hot path on every packet.
const clusterSizeCacheTTL = 30 * time.Second

// resolveLeaseSeconds determines the effective lease duration: the
// inheritance-resolved Option 51 (IP Address Lease Time) value if the admin
// configured one anywhere in the reservation > pool > scope > template >
// global chain, else config.DHCP.DefaultLeaseTime, else a hardcoded 24h
// fallback so the engine never grants a zero-length lease.
func (e *Engine) resolveLeaseSeconds(ctx context.Context, scopeID, poolID, reservationID string) uint32 {
	const optionCodeLeaseTime = 51

	if opt, err := e.store.ResolveOption(ctx, optionCodeLeaseTime, scopeID, poolID, reservationID); err == nil && opt != nil && opt.IsEnabled {
		if v, err := strconv.ParseUint(strings.TrimSpace(opt.Value), 10, 32); err == nil && v > 0 {
			return uint32(v)
		}
	}
	if e.cfg.DHCP.DefaultLeaseTime > 0 {
		return uint32(e.cfg.DHCP.DefaultLeaseTime)
	}
	return 86400
}

// computeTimers derives T1/T2 (in seconds) from a lease duration, per the
// fractions above, clamped so 0 < T1 < T2 < leaseSeconds holds whenever the
// lease is long enough for three distinct whole seconds to exist.
//
// Below three seconds the strict T1 < T2 ordering is arithmetically
// impossible, so it is the one that gives way: T1 = T2 = leaseSeconds-1
// (floored at 1). T2 < leaseSeconds is preserved instead, because that is
// the inequality a client actually acts on — a T2 equal to the lease leaves
// no REBINDING window at all, taking the client straight from BOUND to
// expired. The looser ordering still satisfies dhcp_leases' times_valid
// CHECK, which compares with <=.
//
// The previous clamp chain aimed for strictness at every length and instead
// produced T2 = 0 for a one-second lease — a rebinding time in the past —
// and emitted it to clients as option 59.
func (e *Engine) computeTimers(ctx context.Context, leaseSeconds uint32) (t1, t2 uint32) {
	if leaseSeconds == 0 {
		return 0, 0
	}
	if leaseSeconds < 3 {
		// leaseSeconds is 1 or 2 here; 1 is the floor, so a one-second
		// lease unavoidably reports T2 == leaseSeconds.
		short := leaseSeconds - 1
		if short == 0 {
			short = 1
		}
		return short, short
	}

	frac := e.t1Fraction(ctx)
	t1 = uint32(float64(leaseSeconds) * frac)
	t2 = uint32(float64(leaseSeconds) * t2Fraction)

	// leaseSeconds >= 3 and t2Fraction = 0.875 put t2 at 2 or more before
	// clamping, so t2-1 below can never underflow.
	if t2 >= leaseSeconds {
		t2 = leaseSeconds - 1
	}
	if t1 >= t2 {
		t1 = t2 - 1
	}
	if t1 == 0 {
		t1 = 1
	}
	return t1, t2
}

// t1Fraction picks 40% vs 50% based on whether HA is enabled and the
// cluster currently has 2+ registered servers.
func (e *Engine) t1Fraction(ctx context.Context) float64 {
	if !e.cfg.HA.Enabled || e.cfg.HA.ClusterID == "" {
		return t1FractionStandalone
	}
	if e.currentClusterSize(ctx) >= 2 {
		return t1FractionHA
	}
	return t1FractionStandalone
}

// currentClusterSize returns the number of servers registered in this
// node's HA cluster, cached for clusterSizeCacheTTL to keep this off the
// hot path. On a lookup failure it falls back to the last known good value
// (or 1, meaning "assume standalone") rather than failing lease allocation.
func (e *Engine) currentClusterSize(ctx context.Context) int {
	e.clusterSizeMu.Lock()
	defer e.clusterSizeMu.Unlock()

	if !e.clusterSizeAt.IsZero() && time.Since(e.clusterSizeAt) < clusterSizeCacheTTL {
		return e.clusterSize
	}

	servers, err := e.store.ListServers(ctx, e.cfg.HA.ClusterID)
	if err != nil {
		e.log.Warn("dhcp: list cluster servers failed, keeping previous T1 fraction decision", "cluster_id", e.cfg.HA.ClusterID, "error", err)
		if !e.clusterSizeAt.IsZero() {
			return e.clusterSize
		}
		return 1
	}

	e.clusterSize = len(servers)
	e.clusterSizeAt = time.Now()
	return e.clusterSize
}

// runExpiryReaper periodically marks leases past their lease_end as expired
// via store.ExpireLeasesOlderThan, freeing their addresses back to the
// pool's allocation candidates. Runs until ctx is cancelled (Engine.Stop).
func (e *Engine) runExpiryReaper(ctx context.Context) {
	interval := e.expiryReaperInterval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := e.store.ExpireLeasesOlderThan(ctx, time.Now())
			if err != nil {
				e.log.Error("dhcp: expiry reaper sweep failed", "error", err)
				continue
			}
			if count > 0 {
				e.log.Info("dhcp: expiry reaper swept leases", "count", count)
			}
		}
	}
}
