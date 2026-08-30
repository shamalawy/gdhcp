// postgres_stats.go implements StatsRecorder (in-memory DHCP transaction
// counters flushed into stats_dhcp_transactions), ScopeStore/PoolStore's
// current-utilization queries, DashboardStore, and the
// stats_active_lease_count bookkeeping helper postgres_lease.go calls on
// every claim/release/expiry.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// ---------------------------------------------------------------------------
// StatsRecorder — in-memory transaction counters (hot path) + FlushStats.
// ---------------------------------------------------------------------------

// dhcpTxnCounters buffers one scope's DHCP transaction counts between
// FlushStats cycles. Record* methods run on the DHCP engine's hot path
// (docs/CONTRACTS.md Part 1) and must not block on I/O, so they only mutate
// this in-memory struct under Postgres.txnMu; FlushStats is what actually
// persists them, on whatever cadence the integration layer drives (see
// StatsFlusher's doc comment in postgres.go).
type dhcpTxnCounters struct {
	discover, offer, request, ack, nak, decline, release, noLeases int64
}

// recordTxn gets-or-creates scopeID's counters and applies inc, all under a
// single lock so concurrent Record* calls for the same scope never race.
func (p *Postgres) recordTxn(scopeID string, inc func(*dhcpTxnCounters)) {
	if scopeID == "" {
		return
	}
	p.txnMu.Lock()
	c, ok := p.txnCount[scopeID]
	if !ok {
		c = &dhcpTxnCounters{}
		p.txnCount[scopeID] = c
	}
	inc(c)
	p.txnMu.Unlock()
}

func (p *Postgres) RecordDiscover(scopeID string) {
	p.recordTxn(scopeID, func(c *dhcpTxnCounters) { c.discover++ })
}

func (p *Postgres) RecordOffer(scopeID string) {
	p.recordTxn(scopeID, func(c *dhcpTxnCounters) { c.offer++ })
}

func (p *Postgres) RecordRequest(scopeID string) {
	p.recordTxn(scopeID, func(c *dhcpTxnCounters) { c.request++ })
}

func (p *Postgres) RecordAck(scopeID string) {
	p.recordTxn(scopeID, func(c *dhcpTxnCounters) { c.ack++ })
}

func (p *Postgres) RecordNak(scopeID string) {
	p.recordTxn(scopeID, func(c *dhcpTxnCounters) { c.nak++ })
}

func (p *Postgres) RecordDecline(scopeID string) {
	p.recordTxn(scopeID, func(c *dhcpTxnCounters) { c.decline++ })
}

func (p *Postgres) RecordRelease(scopeID string) {
	p.recordTxn(scopeID, func(c *dhcpTxnCounters) { c.release++ })
}

func (p *Postgres) RecordNoLeases(scopeID string) {
	p.recordTxn(scopeID, func(c *dhcpTxnCounters) { c.noLeases++ })
}

// FlushStats persists every scope's buffered counters into
// stats_dhcp_transactions (today's UTC date row, incremented via
// ON CONFLICT) and resets the in-memory buffer. The integration layer must
// call this periodically (StatsFlusher, e.g. every 10s) — see postgres.go.
// A write failure for one scope (e.g. its stats_dhcp_transactions FK target
// was deleted concurrently) does not drop the others; all such failures are
// joined into the returned error and the failed scopes' counts are lost
// (already removed from the buffer) rather than retried indefinitely.
func (p *Postgres) FlushStats(ctx context.Context) error {
	p.txnMu.Lock()
	snapshot := p.txnCount
	p.txnCount = make(map[string]*dhcpTxnCounters)
	p.txnMu.Unlock()

	if len(snapshot) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	scopeIDs := make([]string, 0, len(snapshot))
	for scopeID, c := range snapshot {
		scopeIDs = append(scopeIDs, scopeID)
		batch.Queue(`
			INSERT INTO stats_dhcp_transactions
				(stat_date, scope_id, discover_count, offer_count, request_count, ack_count,
				 nak_count, decline_count, release_count, no_leases_count)
			VALUES (CURRENT_DATE, $1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (stat_date, scope_id) DO UPDATE SET
				discover_count  = stats_dhcp_transactions.discover_count  + EXCLUDED.discover_count,
				offer_count     = stats_dhcp_transactions.offer_count     + EXCLUDED.offer_count,
				request_count   = stats_dhcp_transactions.request_count   + EXCLUDED.request_count,
				ack_count       = stats_dhcp_transactions.ack_count       + EXCLUDED.ack_count,
				nak_count       = stats_dhcp_transactions.nak_count       + EXCLUDED.nak_count,
				decline_count   = stats_dhcp_transactions.decline_count   + EXCLUDED.decline_count,
				release_count   = stats_dhcp_transactions.release_count   + EXCLUDED.release_count,
				no_leases_count = stats_dhcp_transactions.no_leases_count + EXCLUDED.no_leases_count`,
			scopeID, c.discover, c.offer, c.request, c.ack, c.nak, c.decline, c.release, c.noLeases)
	}

	br := p.pool.SendBatch(ctx, batch)
	var errs []error
	for _, scopeID := range scopeIDs {
		if _, err := br.Exec(); err != nil {
			errs = append(errs, fmt.Errorf("scope %s: %w", scopeID, err))
		}
	}
	if err := br.Close(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("store: flush stats: %d of %d scope(s) failed: %w", len(errs), len(scopeIDs), errors.Join(errs...))
	}
	return nil
}

// GetStats returns scopeID's transaction counts over window, summing the
// persisted daily rows plus whatever is still sitting in the in-memory
// buffer (not yet flushed), so the result stays current between
// FlushStats cycles.
func (p *Postgres) GetStats(ctx context.Context, scopeID string, window time.Duration) (*models.DHCPStats, error) {
	if window <= 0 {
		window = 24 * time.Hour
	}
	since := time.Now().UTC().Add(-window)

	s := &models.DHCPStats{ScopeID: scopeID}
	err := p.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(discover_count),0), COALESCE(SUM(offer_count),0), COALESCE(SUM(request_count),0),
			COALESCE(SUM(ack_count),0), COALESCE(SUM(nak_count),0), COALESCE(SUM(decline_count),0),
			COALESCE(SUM(release_count),0), COALESCE(SUM(no_leases_count),0)
		FROM stats_dhcp_transactions
		WHERE scope_id = $1 AND stat_date >= $2::date`,
		scopeID, since,
	).Scan(&s.DiscoverCount, &s.OfferCount, &s.RequestCount, &s.AckCount, &s.NakCount, &s.DeclineCount, &s.ReleaseCount, &s.NoLeasesCount)
	if err != nil {
		return nil, fmt.Errorf("store: get stats for scope %s: %w", scopeID, err)
	}

	p.txnMu.Lock()
	if c, ok := p.txnCount[scopeID]; ok {
		s.DiscoverCount += c.discover
		s.OfferCount += c.offer
		s.RequestCount += c.request
		s.AckCount += c.ack
		s.NakCount += c.nak
		s.DeclineCount += c.decline
		s.ReleaseCount += c.release
		s.NoLeasesCount += c.noLeases
	}
	p.txnMu.Unlock()

	return s, nil
}

// getStatsAllScopes is GetStats without a scope filter, for
// DashboardStats.DHCPStatsWindow.
func (p *Postgres) getStatsAllScopes(ctx context.Context, window time.Duration) (*models.DHCPStats, error) {
	since := time.Now().UTC().Add(-window)
	s := &models.DHCPStats{}
	err := p.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(discover_count),0), COALESCE(SUM(offer_count),0), COALESCE(SUM(request_count),0),
			COALESCE(SUM(ack_count),0), COALESCE(SUM(nak_count),0), COALESCE(SUM(decline_count),0),
			COALESCE(SUM(release_count),0), COALESCE(SUM(no_leases_count),0)
		FROM stats_dhcp_transactions WHERE stat_date >= $1::date`,
		since,
	).Scan(&s.DiscoverCount, &s.OfferCount, &s.RequestCount, &s.AckCount, &s.NakCount, &s.DeclineCount, &s.ReleaseCount, &s.NoLeasesCount)
	if err != nil {
		return nil, fmt.Errorf("store: get stats (all scopes): %w", err)
	}

	p.txnMu.Lock()
	for _, c := range p.txnCount {
		s.DiscoverCount += c.discover
		s.OfferCount += c.offer
		s.RequestCount += c.request
		s.AckCount += c.ack
		s.NakCount += c.nak
		s.DeclineCount += c.decline
		s.ReleaseCount += c.release
		s.NoLeasesCount += c.noLeases
	}
	p.txnMu.Unlock()

	return s, nil
}

// ---------------------------------------------------------------------------
// stats_active_lease_count bookkeeping (called from postgres_lease.go's
// claim/release/expire paths).
// ---------------------------------------------------------------------------

// adjustActiveLeaseCount applies delta to poolID's cached active-lease count.
// This is a fast-path bookkeeping cache only (clamped at zero so a
// double-decrement race never goes negative) — GetPoolUtilization/
// GetScopeUtilization always recompute the true count from dhcp_leases
// directly, and SampleUtilization reconciles this cache to that true count
// every cycle, so a missed delta self-heals within one sampling interval.
func (p *Postgres) adjustActiveLeaseCount(ctx context.Context, poolID string, delta int64) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO stats_active_lease_count (pool_id, active_count, updated_at)
		VALUES ($1, GREATEST($2, 0), now())
		ON CONFLICT (pool_id) DO UPDATE SET
			active_count = GREATEST(stats_active_lease_count.active_count + $2, 0),
			updated_at = now()`,
		poolID, delta,
	)
	if err != nil {
		return fmt.Errorf("store: adjust active lease count for pool %s: %w", poolID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Current utilization — ScopeStore.GetScopeUtilization,
// PoolStore.GetPoolUtilization, and the shared aggregate they (and
// SampleUtilization/GetDashboardStats) build on.
// ---------------------------------------------------------------------------

// utilizationTotals is the raw aggregate poolUtilizationAgg computes across
// whatever set of pools its WHERE clause matches.
type utilizationTotals struct {
	total            int64 // Usable addresses: range size minus network/broadcast.
	active           int64 // Currently active leases.
	reservedTotal    int64 // Enabled reservations, regardless of current lease state.
	reservedUnleased int64 // Enabled reservations with no active lease right now.
}

// toUtilization converts the raw aggregate into the API-facing shape.
// Available excludes both active leases and reservations that aren't
// currently leased (a leased reservation is already excluded via active),
// matching scanCandidateIPs' (postgres_lease.go) exact candidacy test.
func (t utilizationTotals) toUtilization(entityType models.UtilizationEntityType, entityID string) *models.Utilization {
	available := t.total - t.active - t.reservedUnleased
	if available < 0 {
		available = 0
	}
	var pct float64
	if t.total > 0 {
		pct = float64(t.active) / float64(t.total) * 100
	}
	return &models.Utilization{
		EntityType:            entityType,
		EntityID:              entityID,
		Total:                 t.total,
		Active:                t.active,
		Reserved:              t.reservedTotal,
		Available:             available,
		UtilizationPercentage: pct,
	}
}

// poolUtilizationAgg sums utilizationTotals across every pool whose "po"/
// "sc" rows satisfy wherePools (amendment's "pool: range size minus network/
// broadcast handling" requirement; scope/vrf-level callers pass a broader
// filter and get the sum over all of that entity's pools, i.e. "scope: sum
// of pools" / "vrf: rollup").
func (p *Postgres) poolUtilizationAgg(ctx context.Context, wherePools string, args ...any) (utilizationTotals, error) {
	query := `
		WITH pools AS (
			SELECT po.id
			FROM dhcp_pools po JOIN dhcp_scopes sc ON sc.id = po.scope_id
			WHERE ` + wherePools + `
		),
		per_pool_total AS (
			SELECT po.id,
				(po.range_end - po.range_start + 1)
					- (CASE WHEN set_masklen(network(sc.network::inet), 32) BETWEEN po.range_start AND po.range_end THEN 1 ELSE 0 END)
					- (CASE WHEN set_masklen(broadcast(sc.network::inet), 32) BETWEEN po.range_start AND po.range_end THEN 1 ELSE 0 END)
				AS total
			FROM dhcp_pools po JOIN dhcp_scopes sc ON sc.id = po.scope_id
			WHERE po.id IN (SELECT id FROM pools)
		)
		SELECT
			COALESCE((SELECT SUM(total) FROM per_pool_total), 0),
			(SELECT count(*) FROM dhcp_leases WHERE pool_id IN (SELECT id FROM pools) AND binding_state = 'active'),
			(SELECT count(*) FROM dhcp_reservations WHERE pool_id IN (SELECT id FROM pools) AND is_enabled),
			(SELECT count(*) FROM dhcp_reservations r
				WHERE r.pool_id IN (SELECT id FROM pools) AND r.is_enabled
					AND NOT EXISTS (
						SELECT 1 FROM dhcp_leases l
						WHERE l.pool_id = r.pool_id AND l.ip_address = r.reserved_ip AND l.binding_state = 'active'
					))`
	var t utilizationTotals
	if err := p.pool.QueryRow(ctx, query, args...).Scan(&t.total, &t.active, &t.reservedTotal, &t.reservedUnleased); err != nil {
		return utilizationTotals{}, err
	}
	return t, nil
}

func (p *Postgres) GetScopeUtilization(ctx context.Context, scopeID string) (*models.Utilization, error) {
	t, err := p.poolUtilizationAgg(ctx, `sc.id = $1`, scopeID)
	if err != nil {
		return nil, fmt.Errorf("store: get scope utilization %s: %w", scopeID, err)
	}
	return t.toUtilization(models.UtilizationEntityScope, scopeID), nil
}

func (p *Postgres) GetPoolUtilization(ctx context.Context, poolID string) (*models.Utilization, error) {
	t, err := p.poolUtilizationAgg(ctx, `po.id = $1`, poolID)
	if err != nil {
		return nil, fmt.Errorf("store: get pool utilization %s: %w", poolID, err)
	}
	return t.toUtilization(models.UtilizationEntityPool, poolID), nil
}

// prefixSpaceSQL computes prefixID's address-space view: the address count
// of its own CIDR, and how much of that is covered by its direct (one-level)
// child prefixes. Doubles as GetPrefixUtilization's existence check — no row
// back means prefixID doesn't exist (mapReadError turns that into a 404).
const prefixSpaceSQL = `
	SELECT
		(broadcast(p.prefix::inet) - network(p.prefix::inet) + 1),
		COALESCE((SELECT SUM(broadcast(c.prefix::inet) - network(c.prefix::inet) + 1)
			FROM ipam_prefixes c WHERE c.parent_id = p.id), 0),
		(SELECT count(*) FROM ipam_prefixes c WHERE c.parent_id = p.id)
	FROM ipam_prefixes p WHERE p.id = $1`

// prefixDHCPRollupSQL sums utilizationTotals (plus a scope count) over every
// pool of every scope whose ipam_prefix_id is prefixID or any descendant
// prefix, walked down via a recursive CTE over parent_id.
const prefixDHCPRollupSQL = `
	WITH RECURSIVE descendants AS (
		SELECT id FROM ipam_prefixes WHERE id = $1
		UNION ALL
		SELECT c.id FROM ipam_prefixes c JOIN descendants d ON c.parent_id = d.id
	),
	scope_ids AS (
		SELECT id FROM dhcp_scopes WHERE ipam_prefix_id IN (SELECT id FROM descendants)
	),
	pools AS (
		SELECT po.id, po.range_start, po.range_end, sc.network
		FROM dhcp_pools po JOIN dhcp_scopes sc ON sc.id = po.scope_id
		WHERE po.scope_id IN (SELECT id FROM scope_ids)
	),
	pool_totals AS (
		SELECT id,
			(range_end - range_start + 1)
				- (CASE WHEN set_masklen(network(network::inet), 32) BETWEEN range_start AND range_end THEN 1 ELSE 0 END)
				- (CASE WHEN set_masklen(broadcast(network::inet), 32) BETWEEN range_start AND range_end THEN 1 ELSE 0 END)
			AS total
		FROM pools
	)
	SELECT
		COALESCE((SELECT SUM(total) FROM pool_totals), 0),
		(SELECT count(*) FROM dhcp_leases WHERE pool_id IN (SELECT id FROM pools) AND binding_state = 'active'),
		(SELECT count(*) FROM dhcp_reservations WHERE pool_id IN (SELECT id FROM pools) AND is_enabled),
		(SELECT count(*) FROM dhcp_reservations r
			WHERE r.pool_id IN (SELECT id FROM pools) AND r.is_enabled
				AND NOT EXISTS (
					SELECT 1 FROM dhcp_leases l
					WHERE l.pool_id = r.pool_id AND l.ip_address = r.reserved_ip AND l.binding_state = 'active'
				)),
		(SELECT count(*) FROM scope_ids)`

// GetPrefixUtilization computes prefixID's utilization for
// GET /api/v1/prefixes/{id}/utilization: a DHCP view rolled up over every
// scope anywhere beneath this prefix (in the same shape
// GetScopeUtilization/GetPoolUtilization use), plus an address-space view of
// how much of the prefix's own CIDR its direct children cover. Every prefix
// type gets both views; a subnet with no scope reports a zeroed-out DHCP
// view alongside a still-valid space view, rather than an error.
func (p *Postgres) GetPrefixUtilization(ctx context.Context, prefixID string) (*models.PrefixUtilization, error) {
	pu := &models.PrefixUtilization{PrefixID: prefixID}

	if err := p.pool.QueryRow(ctx, prefixSpaceSQL, prefixID).Scan(&pu.SpaceTotal, &pu.SpaceAllocated, &pu.ChildPrefixCount); err != nil {
		return nil, mapReadError(err, "get prefix utilization "+prefixID)
	}
	if pu.SpaceTotal > 0 {
		pu.SpacePercentage = float64(pu.SpaceAllocated) / float64(pu.SpaceTotal) * 100
	}

	var t utilizationTotals
	if err := p.pool.QueryRow(ctx, prefixDHCPRollupSQL, prefixID).Scan(
		&t.total, &t.active, &t.reservedTotal, &t.reservedUnleased, &pu.ScopeCount,
	); err != nil {
		return nil, fmt.Errorf("store: get prefix utilization %s: dhcp rollup: %w", prefixID, err)
	}
	u := t.toUtilization(models.UtilizationEntityPrefix, prefixID)
	pu.Total, pu.Active, pu.Reserved, pu.Available, pu.UtilizationPercentage = u.Total, u.Active, u.Reserved, u.Available, u.UtilizationPercentage

	return pu, nil
}

// ---------------------------------------------------------------------------
// StatsRecorder.SampleUtilization — point-in-time snapshot for every pool/
// scope/vrf into stats_utilization_samples, active-lease-count
// reconciliation, and the stats_utilization_hourly rollup.
// ---------------------------------------------------------------------------

// sampleUtilizationPoolsSQL computes every pool's utilizationTotals plus its
// owning scope_id/vrf_id in one query, so SampleUtilization can aggregate to
// scope- and vrf-level in Go without N further round trips.
const sampleUtilizationPoolsSQL = `
	SELECT po.id, po.scope_id, sc.vrf_id, sc.ipam_prefix_id,
		(po.range_end - po.range_start + 1)
			- (CASE WHEN set_masklen(network(sc.network::inet), 32) BETWEEN po.range_start AND po.range_end THEN 1 ELSE 0 END)
			- (CASE WHEN set_masklen(broadcast(sc.network::inet), 32) BETWEEN po.range_start AND po.range_end THEN 1 ELSE 0 END)
			AS total,
		COALESCE(ac.n, 0), COALESCE(rc.n, 0), COALESCE(ruc.n, 0)
	FROM dhcp_pools po
	JOIN dhcp_scopes sc ON sc.id = po.scope_id
	LEFT JOIN (SELECT pool_id, count(*) AS n FROM dhcp_leases WHERE binding_state = 'active' GROUP BY pool_id) ac
		ON ac.pool_id = po.id
	LEFT JOIN (SELECT pool_id, count(*) AS n FROM dhcp_reservations WHERE is_enabled GROUP BY pool_id) rc
		ON rc.pool_id = po.id
	LEFT JOIN (
		SELECT r.pool_id, count(*) AS n
		FROM dhcp_reservations r
		WHERE r.is_enabled
			AND NOT EXISTS (SELECT 1 FROM dhcp_leases l WHERE l.pool_id = r.pool_id AND l.ip_address = r.reserved_ip AND l.binding_state = 'active')
		GROUP BY r.pool_id
	) ruc ON ruc.pool_id = po.id`

// hourlyRollupSQL recomputes stats_utilization_hourly for the current hour
// from every stats_utilization_samples row taken so far this hour, across
// every entity at once — a single statement rather than one per entity.
const hourlyRollupSQL = `
	INSERT INTO stats_utilization_hourly
		(sample_hour, entity_type, entity_id, total_addresses,
		 used_addresses_avg, used_addresses_max, used_addresses_min,
		 utilization_percentage_avg, utilization_percentage_max, utilization_percentage_min)
	SELECT date_trunc('hour', now()), entity_type, entity_id,
		max(total_addresses),
		avg(used_addresses), max(used_addresses), min(used_addresses),
		avg(utilization_percentage), max(utilization_percentage), min(utilization_percentage)
	FROM stats_utilization_samples
	WHERE sample_timestamp >= date_trunc('hour', now())
	GROUP BY entity_type, entity_id
	ON CONFLICT (sample_hour, entity_type, entity_id) DO UPDATE SET
		total_addresses             = EXCLUDED.total_addresses,
		used_addresses_avg          = EXCLUDED.used_addresses_avg,
		used_addresses_max          = EXCLUDED.used_addresses_max,
		used_addresses_min          = EXCLUDED.used_addresses_min,
		utilization_percentage_avg  = EXCLUDED.utilization_percentage_avg,
		utilization_percentage_max  = EXCLUDED.utilization_percentage_max,
		utilization_percentage_min  = EXCLUDED.utilization_percentage_min`

func (p *Postgres) SampleUtilization(ctx context.Context) error {
	rows, err := p.pool.Query(ctx, sampleUtilizationPoolsSQL)
	if err != nil {
		return fmt.Errorf("store: sample utilization: query pools: %w", err)
	}
	type poolRow struct {
		poolID, scopeID, vrfID, ipamPrefixID string
		t                                    utilizationTotals
	}
	var poolRows []poolRow
	for rows.Next() {
		var r poolRow
		if err := rows.Scan(&r.poolID, &r.scopeID, &r.vrfID, &r.ipamPrefixID, &r.t.total, &r.t.active, &r.t.reservedTotal, &r.t.reservedUnleased); err != nil {
			rows.Close()
			return fmt.Errorf("store: sample utilization: scan pool: %w", err)
		}
		poolRows = append(poolRows, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("store: sample utilization: %w", err)
	}
	rows.Close()

	type agg struct {
		vrfID        string
		ipamPrefixID string
		t            utilizationTotals
	}
	scopeAgg := make(map[string]agg)
	vrfAgg := make(map[string]utilizationTotals)
	for _, r := range poolRows {
		sa := scopeAgg[r.scopeID]
		sa.vrfID = r.vrfID
		sa.ipamPrefixID = r.ipamPrefixID
		sa.t.total += r.t.total
		sa.t.active += r.t.active
		sa.t.reservedTotal += r.t.reservedTotal
		sa.t.reservedUnleased += r.t.reservedUnleased
		scopeAgg[r.scopeID] = sa

		va := vrfAgg[r.vrfID]
		va.total += r.t.total
		va.active += r.t.active
		va.reservedTotal += r.t.reservedTotal
		va.reservedUnleased += r.t.reservedUnleased
		vrfAgg[r.vrfID] = va
	}

	batch := &pgx.Batch{}
	n := 0
	queueSample := func(entityType models.UtilizationEntityType, entityID string, t utilizationTotals) *models.Utilization {
		u := t.toUtilization(entityType, entityID)
		batch.Queue(`
			INSERT INTO stats_utilization_samples
				(entity_type, entity_id, total_addresses, used_addresses, reserved_addresses, available_addresses, utilization_percentage)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			entityType, entityID, u.Total, u.Active, u.Reserved, u.Available, u.UtilizationPercentage)
		n++
		return u
	}

	for _, r := range poolRows {
		queueSample(models.UtilizationEntityPool, r.poolID, r.t)
		batch.Queue(`
			INSERT INTO stats_active_lease_count (pool_id, active_count, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (pool_id) DO UPDATE SET active_count = EXCLUDED.active_count, updated_at = now()`,
			r.poolID, r.t.active)
		n++
	}
	for scopeID, sa := range scopeAgg {
		queueSample(models.UtilizationEntityScope, scopeID, sa.t)
	}
	for vrfID, t := range vrfAgg {
		queueSample(models.UtilizationEntityVRF, vrfID, t)
	}

	// Prefix-level rollup, for EVERY node of the IPAM tree (not just subnets
	// with a scope): first sum each prefix's OWN scopes (a subnet can have
	// more than one, e.g. one per cluster, hence summing rather than the old
	// per-scope loop's overwrite), then roll that up through ancestors via
	// one query for the whole prefix tree — no N+1 — so a container/
	// aggregate's totals are every descendant scope's numbers combined.
	type prefixAgg struct {
		t          utilizationTotals
		scopeCount int
	}
	directAgg := make(map[string]prefixAgg)
	for _, sa := range scopeAgg {
		if sa.ipamPrefixID == "" {
			continue
		}
		d := directAgg[sa.ipamPrefixID]
		d.t.total += sa.t.total
		d.t.active += sa.t.active
		d.t.reservedTotal += sa.t.reservedTotal
		d.t.reservedUnleased += sa.t.reservedUnleased
		d.scopeCount++
		directAgg[sa.ipamPrefixID] = d
	}

	prefixRows, err := p.pool.Query(ctx, `SELECT id, parent_id FROM ipam_prefixes`)
	if err != nil {
		return fmt.Errorf("store: sample utilization: query prefixes: %w", err)
	}
	var allPrefixIDs []string
	children := make(map[string][]string)
	for prefixRows.Next() {
		var id string
		var parentID *string
		if err := prefixRows.Scan(&id, &parentID); err != nil {
			prefixRows.Close()
			return fmt.Errorf("store: sample utilization: scan prefix: %w", err)
		}
		allPrefixIDs = append(allPrefixIDs, id)
		if parentID != nil {
			children[*parentID] = append(children[*parentID], id)
		}
	}
	if err := prefixRows.Err(); err != nil {
		prefixRows.Close()
		return fmt.Errorf("store: sample utilization: %w", err)
	}
	prefixRows.Close()

	rollupMemo := make(map[string]prefixAgg, len(allPrefixIDs))
	var rollup func(id string) prefixAgg
	rollup = func(id string) prefixAgg {
		if v, ok := rollupMemo[id]; ok {
			return v
		}
		agg := directAgg[id]
		for _, childID := range children[id] {
			c := rollup(childID)
			agg.t.total += c.t.total
			agg.t.active += c.t.active
			agg.t.reservedTotal += c.t.reservedTotal
			agg.t.reservedUnleased += c.t.reservedUnleased
			agg.scopeCount += c.scopeCount
		}
		rollupMemo[id] = agg
		return agg
	}

	for _, id := range allPrefixIDs {
		agg := rollup(id)
		u := queueSample(models.UtilizationEntityPrefix, id, agg.t)
		// docs/db/schema.sql: ipam_prefixes.utilization_percentage is
		// "Cached from stats" — GET /ipam/tree and the sidebar tree surface
		// it per node. NULL only when no descendant (or the prefix itself)
		// has a scope at all; a scope with zero pools still resolves to 0,
		// distinguishing "empty" from "never had a scope".
		if agg.scopeCount > 0 {
			pct := int(math.Round(u.UtilizationPercentage))
			batch.Queue(`UPDATE ipam_prefixes SET utilization_percentage = $1 WHERE id = $2`, pct, id)
		} else {
			batch.Queue(`UPDATE ipam_prefixes SET utilization_percentage = NULL WHERE id = $1`, id)
		}
		n++
	}

	if n > 0 {
		br := p.pool.SendBatch(ctx, batch)
		var errs []error
		for i := 0; i < n; i++ {
			if _, err := br.Exec(); err != nil {
				errs = append(errs, err)
			}
		}
		if err := br.Close(); err != nil {
			errs = append(errs, err)
		}
		if len(errs) > 0 {
			return fmt.Errorf("store: sample utilization: %d write(s) failed: %w", len(errs), errors.Join(errs...))
		}
	}

	if _, err := p.pool.Exec(ctx, hourlyRollupSQL); err != nil {
		return fmt.Errorf("store: sample utilization: hourly rollup: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// DashboardStore
// ---------------------------------------------------------------------------

// dashboardTopScopesLimit is how many scopes GetDashboardStats.TopUtilizedScopes
// returns.
const dashboardTopScopesLimit = 5

func (p *Postgres) GetDashboardStats(ctx context.Context, window time.Duration) (*models.DashboardStats, error) {
	s := &models.DashboardStats{Timestamp: time.Now().UTC()}

	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM vrfs`).Scan(&s.TotalVRFs); err != nil {
		return nil, fmt.Errorf("store: dashboard stats: count vrfs: %w", err)
	}
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_scopes`).Scan(&s.TotalScopes); err != nil {
		return nil, fmt.Errorf("store: dashboard stats: count scopes: %w", err)
	}
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_pools`).Scan(&s.TotalPools); err != nil {
		return nil, fmt.Errorf("store: dashboard stats: count pools: %w", err)
	}

	totals, err := p.poolUtilizationAgg(ctx, `TRUE`)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard stats: utilization totals: %w", err)
	}
	u := totals.toUtilization("", "")
	s.TotalAddresses = u.Total
	s.TotalActiveLeases = u.Active
	s.TotalReservedAddresses = u.Reserved
	s.TotalAvailableAddresses = u.Available
	s.OverallUtilizationPercentage = u.UtilizationPercentage

	if window <= 0 {
		window = 24 * time.Hour
	}
	dhcpStats, err := p.getStatsAllScopes(ctx, window)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard stats: transaction window: %w", err)
	}
	s.DHCPStatsWindow = dhcpStats

	clusters, err := p.ListClusters(ctx, ListParams{Limit: maxListLimit})
	if err != nil {
		return nil, fmt.Errorf("store: dashboard stats: list clusters: %w", err)
	}
	for _, c := range clusters.Items {
		health, err := p.GetClusterHealth(ctx, c.ID)
		if err != nil {
			slog.Default().Warn("store: dashboard stats: cluster health failed", "cluster_id", c.ID, "error", err)
			continue
		}
		s.ClusterHealth = append(s.ClusterHealth, health)
	}

	top, err := p.topUtilizedScopes(ctx, dashboardTopScopesLimit)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard stats: top scopes: %w", err)
	}
	s.TopUtilizedScopes = top

	return s, nil
}

// topUtilizedScopes returns the limit scopes with the highest current
// utilization percentage, for GetDashboardStats.TopUtilizedScopes.
func (p *Postgres) topUtilizedScopes(ctx context.Context, limit int) ([]*models.ScopeUtilizationSummary, error) {
	rows, err := p.pool.Query(ctx, `
		WITH pool_calc AS (
			SELECT po.scope_id,
				(po.range_end - po.range_start + 1)
					- (CASE WHEN set_masklen(network(sc.network::inet), 32) BETWEEN po.range_start AND po.range_end THEN 1 ELSE 0 END)
					- (CASE WHEN set_masklen(broadcast(sc.network::inet), 32) BETWEEN po.range_start AND po.range_end THEN 1 ELSE 0 END)
				AS total
			FROM dhcp_pools po JOIN dhcp_scopes sc ON sc.id = po.scope_id
		),
		scope_totals AS (
			SELECT scope_id, SUM(total) AS total FROM pool_calc GROUP BY scope_id
		),
		active_calc AS (
			SELECT po.scope_id, count(l.id) AS n
			FROM dhcp_pools po
			LEFT JOIN dhcp_leases l ON l.pool_id = po.id AND l.binding_state = 'active'
			GROUP BY po.scope_id
		)
		SELECT sc.id, sc.name, COALESCE(st.total, 0), COALESCE(ac.n, 0),
			CASE WHEN COALESCE(st.total, 0) > 0 THEN (COALESCE(ac.n, 0)::float8 / st.total) * 100 ELSE 0 END AS pct
		FROM dhcp_scopes sc
		LEFT JOIN scope_totals st ON st.scope_id = sc.id
		LEFT JOIN active_calc ac ON ac.scope_id = sc.id
		ORDER BY pct DESC, sc.name
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: top utilized scopes: %w", err)
	}
	defer rows.Close()

	var out []*models.ScopeUtilizationSummary
	for rows.Next() {
		var s models.ScopeUtilizationSummary
		if err := rows.Scan(&s.ScopeID, &s.ScopeName, &s.Total, &s.Active, &s.UtilizationPercentage); err != nil {
			return nil, fmt.Errorf("store: top utilized scopes: scan: %w", err)
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// GetUtilizationChart returns entityID's hourly utilization history over the
// last days days, for GET /api/v1/dashboard/utilization-chart.
func (p *Postgres) GetUtilizationChart(ctx context.Context, entityType models.UtilizationEntityType, entityID string, days int) ([]*models.ChartPoint, error) {
	if days <= 0 {
		days = 7
	}
	rows, err := p.pool.Query(ctx, `
		SELECT sample_hour, COALESCE(utilization_percentage_avg, 0), COALESCE(used_addresses_avg, 0), total_addresses
		FROM stats_utilization_hourly
		WHERE entity_type = $1 AND entity_id = $2 AND sample_hour >= now() - make_interval(days => $3)
		ORDER BY sample_hour`, entityType, entityID, days)
	if err != nil {
		return nil, fmt.Errorf("store: utilization chart for %s %s: %w", entityType, entityID, err)
	}
	defer rows.Close()

	var out []*models.ChartPoint
	for rows.Next() {
		var cp models.ChartPoint
		var usedAvg float64
		if err := rows.Scan(&cp.Timestamp, &cp.UtilizationPercentage, &usedAvg, &cp.TotalAddresses); err != nil {
			return nil, fmt.Errorf("store: utilization chart for %s %s: scan: %w", entityType, entityID, err)
		}
		cp.UsedAddresses = int64(usedAvg + 0.5)
		out = append(out, &cp)
	}
	return out, rows.Err()
}
