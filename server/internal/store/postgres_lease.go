// postgres_lease.go implements LeaseStore, LeaseQueryStore,
// LeaseAdminStore, ReservationStore, and AllocationHelperStore.NextAvailableIP:
// DHCP lease allocation per amendment A6, lease lifecycle/query, and
// reservations.
package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

const leaseSelectCols = `id, pool_id, ip_address, client_mac, client_id, client_hostname,
	binding_state, binding_server_id, lease_start, lease_end, renewal_time, rebinding_time,
	created_at, updated_at`

// scanLease scans one row shaped like leaseSelectCols.
func scanLease(row pgx.Row) (*models.DHCPLease, error) {
	var l models.DHCPLease
	var mac net.HardwareAddr
	var clientID []byte
	if err := row.Scan(
		&l.ID, &l.PoolID, &l.IPAddress, &mac, &clientID, &l.ClientHostname,
		&l.BindingState, &l.BindingServerID, &l.LeaseStart, &l.LeaseEnd, &l.RenewalTime, &l.RebindingTime,
		&l.CreatedAt, &l.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(mac) > 0 {
		hw := models.HardwareAddr(mac)
		l.ClientMAC = &hw
	}
	l.ClientID = clientID
	return &l, nil
}

// scanLeaseWithPriorState scans leaseSelectCols plus a trailing prior_state
// text column (see claimLeaseIP's reclaim query).
func scanLeaseWithPriorState(row pgx.Row) (*models.DHCPLease, string, error) {
	var l models.DHCPLease
	var mac net.HardwareAddr
	var clientID []byte
	var priorState string
	if err := row.Scan(
		&l.ID, &l.PoolID, &l.IPAddress, &mac, &clientID, &l.ClientHostname,
		&l.BindingState, &l.BindingServerID, &l.LeaseStart, &l.LeaseEnd, &l.RenewalTime, &l.RebindingTime,
		&l.CreatedAt, &l.UpdatedAt, &priorState,
	); err != nil {
		return nil, "", err
	}
	if len(mac) > 0 {
		hw := models.HardwareAddr(mac)
		l.ClientMAC = &hw
	}
	l.ClientID = clientID
	return &l, priorState, nil
}

// decodeClientID interprets AllocateLease/RenewLease's clientID string
// parameter as hex-encoded raw DHCP client-identifier bytes (option 61),
// the conventional wire-safe representation for an opaque byte string
// crossing a Go string API boundary. Falls back to the raw bytes of s if it
// isn't valid hex, so a caller passing a plain opaque string still works.
// See notes_for_integration for the DHCP engine unit.
func decodeClientID(s string) []byte {
	if s == "" {
		return nil
	}
	if b, err := hex.DecodeString(s); err == nil {
		return b
	}
	return []byte(s)
}

func encodeClientID(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}

// leaseIdentity reconstructs the parts of a client's identity that a lease
// row actually records, so a reservation's binding key can be tested against
// the client currently holding an address. A lease stores only the MAC, the
// option 61 client-ID and the hostname -- the relay and class identifiers are
// request-scoped and are not persisted -- so a reservation keyed by one of
// those cannot be proven to belong to this client and is treated as a
// conflict, which is the safe direction.
func leaseIdentity(clientMAC, clientID string, hostname *string) models.ClientIdentity {
	ident := models.ClientIdentity{MAC: clientMAC, ClientID: clientID}
	if hostname != nil {
		ident.Hostname = *hostname
	}
	return ident
}

// claimLeaseIP is the atomic claim primitive behind amendment A6: try a
// bare INSERT ... ON CONFLICT DO NOTHING first (the common case: this IP
// has never had a lease row in this pool), and if that IP already has a row,
// fall back to a conditional UPDATE that only succeeds when the existing row
// is expired/released, or is this same client's own active lease
// (renew-in-place affinity). Returns claimed=false (not an error) when
// neither statement affected a row — the caller should try the next
// candidate.
func (p *Postgres) claimLeaseIP(
	ctx context.Context, poolID string, ip net.IP, mac net.HardwareAddr, clientID []byte,
	hostname *string, serverID *string, start, end time.Time, renewal, rebind time.Time,
) (*models.DHCPLease, bool, error) {
	var macArg any = mac
	if len(mac) == 0 {
		macArg = nil
	}
	var cidArg any = clientID
	if len(clientID) == 0 {
		cidArg = nil
	}
	var serverArg any
	if serverID != nil {
		serverArg = *serverID
	}

	row := p.pool.QueryRow(ctx, `
		INSERT INTO dhcp_leases (pool_id, ip_address, client_mac, client_id, client_hostname,
			binding_state, binding_server_id, lease_start, lease_end, renewal_time, rebinding_time)
		VALUES ($1, $2, $3, $4, $5, 'active', $6, $7, $8, $9, $10)
		ON CONFLICT (pool_id, ip_address) DO NOTHING
		RETURNING `+leaseSelectCols,
		poolID, ip, macArg, cidArg, hostname, serverArg, start, end, renewal, rebind,
	)
	if lease, err := scanLease(row); err == nil {
		if err := p.adjustActiveLeaseCount(ctx, poolID, 1); err != nil {
			logStatsWarn("adjust active lease count (insert)", err)
		}
		return lease, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("store: claim lease %s in pool %s: %w", ip, poolID, err)
	}

	row = p.pool.QueryRow(ctx, `
		WITH prior AS (
			SELECT binding_state FROM dhcp_leases WHERE pool_id = $1 AND ip_address = $2
		)
		UPDATE dhcp_leases SET
			client_mac = $3, client_id = $4, client_hostname = $5,
			binding_state = 'active', binding_server_id = $6,
			lease_start = $7, lease_end = $8, renewal_time = $9, rebinding_time = $10,
			updated_at = now()
		WHERE pool_id = $1 AND ip_address = $2
			AND (
				binding_state IN ('expired', 'released')
				OR (binding_state = 'active' AND client_mac IS NOT DISTINCT FROM $3 AND client_id IS NOT DISTINCT FROM $4)
			)
		RETURNING `+leaseSelectCols+`, (SELECT binding_state FROM prior)`,
		poolID, ip, macArg, cidArg, hostname, serverArg, start, end, renewal, rebind,
	)
	lease, priorState, err := scanLeaseWithPriorState(row)
	if err == nil {
		if priorState != string(models.LeaseStateActive) {
			if err := p.adjustActiveLeaseCount(ctx, poolID, 1); err != nil {
				logStatsWarn("adjust active lease count (reclaim)", err)
			}
		}
		return lease, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("store: reclaim lease %s in pool %s: %w", ip, poolID, err)
}

// errAllocationRoundFailed signals "nothing claimable this round, try
// again" internally to AllocateLease's retry loop; it never escapes this
// file.
var errAllocationRoundFailed = errors.New("allocation round failed")

// candidateBatchSize is how many free candidate IPs AllocateLease pulls per
// round (amendment A6: "a few candidates at a time").
const candidateBatchSize = 8

// AllocateLease implements amendment A6's algorithm: reservation match,
// then client renew-affinity, then a scan of unused candidate IPs, each
// claimed via claimLeaseIP; up to 3 rounds before giving up.
func (p *Postgres) AllocateLease(ctx context.Context, poolID string, ident models.ClientIdentity, hostname *string) (*models.DHCPLease, error) {
	var mac net.HardwareAddr
	if ident.MAC != "" {
		parsed, err := net.ParseMAC(ident.MAC)
		if err != nil {
			return nil, fmt.Errorf("store: allocate lease: invalid client MAC %q: %w", ident.MAC, models.ErrInvalidInput)
		}
		mac = parsed
	}
	cidBytes := decodeClientID(ident.ClientID)

	pool, err := p.GetPoolByID(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("store: allocate lease: load pool %s: %w", poolID, err)
	}

	leaseTime := 24 * time.Hour
	start := time.Now().UTC()
	end := start.Add(leaseTime)
	renewal := start.Add(leaseTime / 2)
	rebind := start.Add(leaseTime * 7 / 8)

	const maxRounds = 3
	for round := 0; round < maxRounds; round++ {
		lease, err := p.tryAllocateRound(ctx, pool.ID, mac, cidBytes, ident, hostname, start, end, renewal, rebind)
		if err == nil {
			return lease, nil
		}
		if !errors.Is(err, errAllocationRoundFailed) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("store: allocate lease in pool %s: %w", poolID, models.ErrNoLeases)
}

func (p *Postgres) tryAllocateRound(
	ctx context.Context, poolID string, mac net.HardwareAddr, cidBytes []byte, ident models.ClientIdentity, hostname *string,
	start, end, renewal, rebind time.Time,
) (*models.DHCPLease, error) {
	// 1. Reservation match using multi-identifier precedence order.
	if keys := ident.BindingKeys(); len(keys) > 0 {
		res, err := p.reservationForKeys(ctx, poolID, keys)
		if err != nil {
			return nil, fmt.Errorf("store: allocate lease: lookup reservation: %w", err)
		}
		if res != nil && res.IsEnabled {
			// Preference: use client-provided hostname, fall back to reservation's.
			allocationHostname := hostname
			if allocationHostname == nil {
				allocationHostname = res.Hostname
			}
			lease, claimed, err := p.claimLeaseIP(ctx, poolID, res.ReservedIP, mac, cidBytes, allocationHostname, nil, start, end, renewal, rebind)
			if err != nil {
				return nil, err
			}
			if claimed {
				return lease, nil
			}
			return nil, errAllocationRoundFailed
		}
	}

	// 2. Client renew affinity: this client's most recent lease in the pool,
	// of any state.
	if existing, err := p.mostRecentClientLease(ctx, poolID, mac, cidBytes); err != nil {
		return nil, fmt.Errorf("store: allocate lease: lookup client affinity: %w", err)
	} else if existing != nil {
		ok, err := p.CanAllocateInPool(ctx, poolID, existing.IPAddress)
		if err != nil {
			return nil, fmt.Errorf("store: allocate lease: validate affinity IP: %w", err)
		}
		if ok || existing.BindingState == models.LeaseStateActive {
			// Preference: use client-provided hostname, fall back to existing lease's.
			renewHostname := hostname
			if renewHostname == nil {
				renewHostname = existing.ClientHostname
			}
			lease, claimed, err := p.claimLeaseIP(ctx, poolID, existing.IPAddress, mac, cidBytes, renewHostname, nil, start, end, renewal, rebind)
			if err != nil {
				return nil, err
			}
			if claimed {
				return lease, nil
			}
		}
	}

	// 3. Scan free candidates and claim the first available.
	candidates, err := p.scanCandidateIPs(ctx, poolID, candidateBatchSize)
	if err != nil {
		return nil, fmt.Errorf("store: allocate lease: scan candidates: %w", err)
	}
	for _, ip := range candidates {
		lease, claimed, err := p.claimLeaseIP(ctx, poolID, ip, mac, cidBytes, hostname, nil, start, end, renewal, rebind)
		if err != nil {
			return nil, err
		}
		if claimed {
			return lease, nil
		}
	}

	return nil, errAllocationRoundFailed
}

// reservationForKeys returns the first enabled reservation in poolID whose
// binding_key matches any of keys, in the precedence order of keys
// (which must be ClientIdentity.BindingKeys()' order).
func (p *Postgres) reservationForKeys(ctx context.Context, poolID string, keys []string) (*models.DHCPReservation, error) {
	// Build a CASE/array_position expression to sort by key precedence.
	// array_position returns 1-based index; ORDER BY precedence position.
	row := p.pool.QueryRow(ctx, `
		SELECT id, pool_id, hostname, description, reserved_ip, binding_key, binding_type, is_enabled,
			created_at, updated_at, created_by_user_id, updated_by_user_id
		FROM dhcp_reservations
		WHERE pool_id = $1 AND binding_key = ANY($2) AND is_enabled
		ORDER BY array_position($2, binding_key)
		LIMIT 1`, poolID, keys)
	r, err := scanReservation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// ReservationsForKeys is the public LeaseStore method: returns the first
// enabled reservation in poolID whose binding_key matches any of the given
// keys in precedence order. Implements the store interface.
func (p *Postgres) ReservationsForKeys(ctx context.Context, poolID string, keys []string) (*models.DHCPReservation, error) {
	return p.reservationForKeys(ctx, poolID, keys)
}

// mostRecentClientLease returns this client's latest lease (any state) in
// poolID, for renew-affinity.
func (p *Postgres) mostRecentClientLease(ctx context.Context, poolID string, mac net.HardwareAddr, clientID []byte) (*models.DHCPLease, error) {
	var macArg any = mac
	if len(mac) == 0 {
		macArg = nil
	}
	var cidArg any = clientID
	if len(clientID) == 0 {
		cidArg = nil
	}
	if macArg == nil && cidArg == nil {
		return nil, nil
	}
	row := p.pool.QueryRow(ctx, `
		SELECT `+leaseSelectCols+`
		FROM dhcp_leases
		WHERE pool_id = $1
			AND ((client_mac = $2 AND $2 IS NOT NULL) OR (client_id = $3 AND $3 IS NOT NULL))
		ORDER BY updated_at DESC
		LIMIT 1`, poolID, macArg, cidArg)
	lease, err := scanLease(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return lease, err
}

// scanCandidateIPs returns up to limit unused IPs in poolID (no active
// lease, no enabled reservation, not the network/broadcast address of the
// scope's network), in ascending address order. Amendment A6: "SQL against
// generate_series over the range ... a few candidates at a time."
func (p *Postgres) scanCandidateIPs(ctx context.Context, poolID string, limit int) ([]net.IP, error) {
	rows, err := p.pool.Query(ctx, `
		WITH pool_info AS (
			SELECT po.range_start, po.range_end, sc.network
			FROM dhcp_pools po JOIN dhcp_scopes sc ON sc.id = po.scope_id
			WHERE po.id = $1
		)
		SELECT (range_start + gs)::inet AS ip
		FROM pool_info, generate_series(0, (range_end - range_start)) AS gs
		WHERE (range_start + gs)::inet <> set_masklen(network(network::inet), 32)
			AND (range_start + gs)::inet <> set_masklen(broadcast(network::inet), 32)
			AND NOT EXISTS (
				SELECT 1 FROM dhcp_leases l
				WHERE l.pool_id = $1 AND l.ip_address = (range_start + gs)::inet AND l.binding_state = 'active'
			)
			AND NOT EXISTS (
				SELECT 1 FROM dhcp_reservations r
				WHERE r.pool_id = $1 AND r.reserved_ip = (range_start + gs)::inet AND r.is_enabled
			)
		ORDER BY gs
		LIMIT $2`, poolID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ips []net.IP
	for rows.Next() {
		var ip net.IP
		if err := rows.Scan(&ip); err != nil {
			return nil, err
		}
		ips = append(ips, ip)
	}
	return ips, rows.Err()
}

// RenewLease extends lease_end, resetting lease_start to now and recomputing
// renewal/rebinding times at the conventional T1=50%/T2=87.5% marks so the
// dhcp_leases.times_valid CHECK constraint (lease_start <= renewal_time <=
// rebinding_time <= lease_end) always holds regardless of the prior lease's
// timers. The DHCP engine owns actual T1/T2 protocol timing in memory; these
// columns are persisted bookkeeping.
func (p *Postgres) RenewLease(ctx context.Context, leaseID string, clientMAC string, newExpiry time.Time, hostname *string) (*models.DHCPLease, error) {
	var macArg any
	if clientMAC != "" {
		mac, err := net.ParseMAC(clientMAC)
		if err != nil {
			return nil, fmt.Errorf("store: renew lease: invalid client MAC %q: %w", clientMAC, models.ErrInvalidInput)
		}
		macArg = mac
	}

	now := time.Now().UTC()
	if !newExpiry.After(now) {
		return nil, fmt.Errorf("store: renew lease %s: new expiry not in the future: %w", leaseID, models.ErrInvalidInput)
	}
	duration := newExpiry.Sub(now)
	renewal := now.Add(duration / 2)
	rebind := now.Add(duration * 7 / 8)

	row := p.pool.QueryRow(ctx, `
		UPDATE dhcp_leases SET
			lease_start = $2, lease_end = $3, renewal_time = $4, rebinding_time = $5,
			client_hostname = COALESCE($7, client_hostname), updated_at = now()
		WHERE id = $1 AND binding_state = 'active'
			AND (client_mac IS NOT DISTINCT FROM $6)
		RETURNING `+leaseSelectCols,
		leaseID, now, newExpiry, renewal, rebind, macArg, hostname,
	)
	lease, err := scanLease(row)
	if err == nil {
		return lease, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("store: renew lease %s: %w", leaseID, err)
	}
	return nil, fmt.Errorf("store: renew lease %s: %w", leaseID, models.ErrLeaseNotFound)
}

// ReleaseLease marks the lease released. Idempotent: releasing an
// already-released/expired lease, or one that doesn't exist for this MAC,
// is a silent no-op rather than an error — only a MAC mismatch against a
// still-active lease, or a leaseID that never existed, is an error.
func (p *Postgres) ReleaseLease(ctx context.Context, leaseID string, clientMAC string) error {
	var macArg any
	if clientMAC != "" {
		mac, err := net.ParseMAC(clientMAC)
		if err != nil {
			return fmt.Errorf("store: release lease: invalid client MAC %q: %w", clientMAC, models.ErrInvalidInput)
		}
		macArg = mac
	}

	var poolID string
	err := p.pool.QueryRow(ctx, `
		UPDATE dhcp_leases SET binding_state = 'released', updated_at = now()
		WHERE id = $1 AND binding_state = 'active' AND (client_mac IS NOT DISTINCT FROM $2)
		RETURNING pool_id`, leaseID, macArg,
	).Scan(&poolID)
	if err == nil {
		if err := p.adjustActiveLeaseCount(ctx, poolID, -1); err != nil {
			logStatsWarn("adjust active lease count (release)", err)
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("store: release lease %s: %w", leaseID, err)
	}

	var state string
	var mac net.HardwareAddr
	lookupErr := p.pool.QueryRow(ctx, `SELECT binding_state, client_mac FROM dhcp_leases WHERE id = $1`, leaseID).Scan(&state, &mac)
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return fmt.Errorf("store: release lease %s: %w", leaseID, models.ErrLeaseNotFound)
	}
	if lookupErr != nil {
		return fmt.Errorf("store: release lease %s: %w", leaseID, lookupErr)
	}
	if state != string(models.LeaseStateActive) {
		return nil // already released/expired/abandoned: idempotent no-op.
	}
	return fmt.Errorf("store: release lease %s: %w", leaseID, models.ErrLeaseNotFound)
}

// AbandonLease marks a lease as abandoned by this server (see
// store.LeaseStore's doc comment). claimLeaseIP's reclaim UPDATE only
// matches binding_state IN ('expired', 'released'), so — unlike
// ReleaseLease — an abandoned row is not immediately reclaimable by
// AllocateLease's renew-affinity or candidate-scan steps; it becomes
// reclaimable again once ExpireLeasesOlderThan sweeps it past lease_end.
func (p *Postgres) AbandonLease(ctx context.Context, leaseID string, clientMAC string) error {
	var macArg any
	if clientMAC != "" {
		mac, err := net.ParseMAC(clientMAC)
		if err != nil {
			return fmt.Errorf("store: abandon lease: invalid client MAC %q: %w", clientMAC, models.ErrInvalidInput)
		}
		macArg = mac
	}

	var poolID string
	err := p.pool.QueryRow(ctx, `
		UPDATE dhcp_leases SET binding_state = 'abandoned', updated_at = now()
		WHERE id = $1 AND binding_state = 'active' AND (client_mac IS NOT DISTINCT FROM $2)
		RETURNING pool_id`, leaseID, macArg,
	).Scan(&poolID)
	if err == nil {
		if err := p.adjustActiveLeaseCount(ctx, poolID, -1); err != nil {
			logStatsWarn("adjust active lease count (abandon)", err)
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("store: abandon lease %s: %w", leaseID, err)
	}

	var state string
	var mac net.HardwareAddr
	lookupErr := p.pool.QueryRow(ctx, `SELECT binding_state, client_mac FROM dhcp_leases WHERE id = $1`, leaseID).Scan(&state, &mac)
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return fmt.Errorf("store: abandon lease %s: %w", leaseID, models.ErrLeaseNotFound)
	}
	if lookupErr != nil {
		return fmt.Errorf("store: abandon lease %s: %w", leaseID, lookupErr)
	}
	if state != string(models.LeaseStateActive) {
		return nil // already released/expired/abandoned: idempotent no-op.
	}
	return fmt.Errorf("store: abandon lease %s: %w", leaseID, models.ErrLeaseNotFound)
}

// GetLeaseByIP returns the active lease for ip within poolID.
func (p *Postgres) GetLeaseByIP(ctx context.Context, poolID string, ip net.IP) (*models.DHCPLease, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+leaseSelectCols+` FROM dhcp_leases
		WHERE pool_id = $1 AND ip_address = $2 AND binding_state = 'active'`, poolID, ip)
	lease, err := scanLease(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get lease by ip %s in pool %s: %w", ip, poolID, err)
	}
	return lease, nil
}

// GetLeaseByClient returns the active lease for clientMAC/clientID, if any.
func (p *Postgres) GetLeaseByClient(ctx context.Context, ident models.ClientIdentity) (*models.DHCPLease, error) {
	var macArg any
	if ident.MAC != "" {
		mac, err := net.ParseMAC(ident.MAC)
		if err != nil {
			return nil, fmt.Errorf("store: get lease by client: invalid MAC %q: %w", ident.MAC, models.ErrInvalidInput)
		}
		macArg = mac
	}
	var cidArg any
	if cid := decodeClientID(ident.ClientID); len(cid) > 0 {
		cidArg = cid
	}
	if macArg == nil && cidArg == nil {
		return nil, nil
	}
	row := p.pool.QueryRow(ctx, `SELECT `+leaseSelectCols+` FROM dhcp_leases
		WHERE binding_state = 'active'
			AND ((client_mac = $1 AND $1 IS NOT NULL) OR (client_id = $2 AND $2 IS NOT NULL))
		ORDER BY updated_at DESC LIMIT 1`, macArg, cidArg)
	lease, err := scanLease(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get lease by client: %w", err)
	}
	return lease, nil
}

// GetLeasesByPool lists leases in poolID, optionally filtered by state.
func (p *Postgres) GetLeasesByPool(ctx context.Context, poolID string, state string, offset, limit int) ([]*models.DHCPLease, int64, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	var stateArg any
	if state != "" {
		stateArg = state
	}

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_leases WHERE pool_id = $1 AND ($2::text IS NULL OR binding_state = $2)`,
		poolID, stateArg).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count leases in pool %s: %w", poolID, err)
	}

	rows, err := p.pool.Query(ctx, `SELECT `+leaseSelectCols+` FROM dhcp_leases
		WHERE pool_id = $1 AND ($2::text IS NULL OR binding_state = $2)
		ORDER BY ip_address LIMIT $3 OFFSET $4`, poolID, stateArg, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list leases in pool %s: %w", poolID, err)
	}
	defer rows.Close()

	leases, err := collectLeases(rows)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list leases in pool %s: %w", poolID, err)
	}
	return leases, total, nil
}

func collectLeases(rows pgx.Rows) ([]*models.DHCPLease, error) {
	var out []*models.DHCPLease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ExpireLeasesOlderThan marks active/released leases with lease_end < cutoff
// as expired, decrementing stats_active_lease_count for the ones that were
// active.
func (p *Postgres) ExpireLeasesOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	rows, err := p.pool.Query(ctx, `
		UPDATE dhcp_leases SET binding_state = 'expired', updated_at = now()
		WHERE lease_end < $1 AND binding_state = 'active'
		RETURNING pool_id`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: expire active leases: %w", err)
	}
	var expiredActive int64
	perPool := make(map[string]int64)
	for rows.Next() {
		var poolID string
		if err := rows.Scan(&poolID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: expire active leases: %w", err)
		}
		perPool[poolID]++
		expiredActive++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: expire active leases: %w", err)
	}
	for poolID, n := range perPool {
		if err := p.adjustActiveLeaseCount(ctx, poolID, -n); err != nil {
			logStatsWarn("adjust active lease count (expire)", err)
		}
	}

	// 'abandoned' rows swept here alongside 'released' ones: they were
	// already excluded from the active count when AbandonLease set them
	// (unlike the 'active' branch above), so no further count adjustment is
	// needed — this only makes them reclaimable again via claimLeaseIP's
	// binding_state IN ('expired', 'released') WHERE clause, rather than
	// leaving an abandoned address permanently unallocatable.
	tag, err := p.pool.Exec(ctx, `
		UPDATE dhcp_leases SET binding_state = 'expired', updated_at = now()
		WHERE lease_end < $1 AND binding_state IN ('released', 'abandoned')`, cutoff)
	if err != nil {
		return expiredActive, fmt.Errorf("store: expire released leases: %w", err)
	}

	return expiredActive + tag.RowsAffected(), nil
}

// ReservationForLease checks for a reservation matching bindingKey in poolID.
func (p *Postgres) ReservationForLease(ctx context.Context, poolID string, bindingKey string) (*models.DHCPReservation, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT id, pool_id, hostname, description, reserved_ip, binding_key, binding_type, is_enabled,
			created_at, updated_at, created_by_user_id, updated_by_user_id
		FROM dhcp_reservations WHERE pool_id = $1 AND binding_key = $2`, poolID, bindingKey)
	r, err := scanReservation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: reservation for lease in pool %s: %w", poolID, err)
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// LeaseQueryStore
// ---------------------------------------------------------------------------

// ListLeases supports filters "scope_id", "pool_id", "state" plus
// params.Search, matched case-insensitively against the lease's IP text,
// MAC text and client hostname so the UI's one search box covers all three
// without the caller having to guess which field a term belongs to.
func (p *Postgres) ListLeases(ctx context.Context, params ListParams) (ListResult[*models.DHCPLease], error) {
	limit, offset := normalizeListParams(params)
	scopeID := nilIfEmpty(params.Filters["scope_id"])
	poolID := nilIfEmpty(params.Filters["pool_id"])
	state := nilIfEmpty(params.Filters["state"])
	search := nilIfEmpty(strings.TrimSpace(params.Search))

	const where = `
		WHERE ($1::uuid IS NULL OR l.pool_id IN (SELECT id FROM dhcp_pools WHERE scope_id = $1))
			AND ($2::uuid IS NULL OR l.pool_id = $2)
			AND ($3::text IS NULL OR l.binding_state = $3)
			AND ($4::text IS NULL OR (
				host(l.ip_address) ILIKE '%' || $4 || '%'
				OR COALESCE(l.client_mac::text, '') ILIKE '%' || $4 || '%'
				OR COALESCE(l.client_hostname, '') ILIKE '%' || $4 || '%'
			))`

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_leases l`+where, scopeID, poolID, state, search).Scan(&total); err != nil {
		return ListResult[*models.DHCPLease]{}, fmt.Errorf("store: count leases: %w", err)
	}

	order := orderByClause(params.Sort, map[string]string{
		"ip_address": "l.ip_address", "created_at": "l.created_at", "lease_end": "l.lease_end",
		"client_mac": "l.client_mac", "client_hostname": "l.client_hostname", "binding_state": "l.binding_state",
		"updated_at": "l.updated_at",
	}, "l.ip_address ASC")

	rows, err := p.pool.Query(ctx, `SELECT `+prefixCols("l", leaseSelectCols)+` FROM dhcp_leases l`+where+
		` ORDER BY `+order+` LIMIT $5 OFFSET $6`, scopeID, poolID, state, search, limit, offset)
	if err != nil {
		return ListResult[*models.DHCPLease]{}, fmt.Errorf("store: list leases: %w", err)
	}
	defer rows.Close()

	items, err := collectLeases(rows)
	if err != nil {
		return ListResult[*models.DHCPLease]{}, fmt.Errorf("store: list leases: %w", err)
	}
	return ListResult[*models.DHCPLease]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// DeleteLease removes a lease row (LeaseAdminStore). The DELETE returns the
// row's pool and state so an active lease's removal can be reflected in the
// pool's cached active-lease count — otherwise the dashboard would keep
// counting an address that no longer has a lease at all.
func (p *Postgres) DeleteLease(ctx context.Context, id string) error {
	var poolID, state string
	err := p.pool.QueryRow(ctx,
		`DELETE FROM dhcp_leases WHERE id = $1 RETURNING pool_id, binding_state`, id).Scan(&poolID, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("store: delete lease %s: %w", id, models.ErrLeaseNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: delete lease %s: %w", id, err)
	}
	if state == string(models.LeaseStateActive) {
		if err := p.adjustActiveLeaseCount(ctx, poolID, -1); err != nil {
			logStatsWarn("adjust active lease count (delete)", err)
		}
	}
	return nil
}

// ForceRenewLease implements LeaseAdminStore: an operator-driven renewal
// that skips RenewLease's MAC match and active-state precondition, and
// reactivates a lease that had lapsed. The prior state comes back with the
// row so the pool's active count is only incremented when the lease was not
// already active.
func (p *Postgres) ForceRenewLease(ctx context.Context, id string, expiry time.Time) (*models.DHCPLease, error) {
	now := time.Now().UTC()
	if !expiry.After(now) {
		return nil, fmt.Errorf("store: force renew lease %s: new expiry not in the future: %w", id, models.ErrInvalidInput)
	}
	duration := expiry.Sub(now)
	renewal := now.Add(duration / 2)
	rebind := now.Add(duration * 7 / 8)

	row := p.pool.QueryRow(ctx, `
		WITH prior AS (
			SELECT binding_state FROM dhcp_leases WHERE id = $1
		)
		UPDATE dhcp_leases SET
			lease_start = $2, lease_end = $3, renewal_time = $4, rebinding_time = $5,
			binding_state = 'active', updated_at = now()
		WHERE id = $1
		RETURNING `+leaseSelectCols+`, (SELECT binding_state FROM prior)`,
		id, now, expiry, renewal, rebind,
	)
	lease, priorState, err := scanLeaseWithPriorState(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("store: force renew lease %s: %w", id, models.ErrLeaseNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: force renew lease %s: %w", id, err)
	}
	if priorState != string(models.LeaseStateActive) {
		if err := p.adjustActiveLeaseCount(ctx, lease.PoolID, 1); err != nil {
			logStatsWarn("adjust active lease count (force renew)", err)
		}
	}
	return lease, nil
}

func (p *Postgres) GetLease(ctx context.Context, id string) (*models.DHCPLease, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+leaseSelectCols+` FROM dhcp_leases WHERE id = $1`, id)
	l, err := scanLease(row)
	if err != nil {
		return nil, mapReadError(err, "get lease "+id)
	}
	return l, nil
}

func (p *Postgres) ListLeasesByPool(ctx context.Context, poolID string, params ListParams) (ListResult[*models.DHCPLease], error) {
	limit, offset := normalizeListParams(params)
	state := nilIfEmpty(params.Filters["state"])

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_leases WHERE pool_id = $1 AND ($2::text IS NULL OR binding_state = $2)`,
		poolID, state).Scan(&total); err != nil {
		return ListResult[*models.DHCPLease]{}, fmt.Errorf("store: count leases in pool %s: %w", poolID, err)
	}

	rows, err := p.pool.Query(ctx, `SELECT `+leaseSelectCols+` FROM dhcp_leases
		WHERE pool_id = $1 AND ($2::text IS NULL OR binding_state = $2)
		ORDER BY ip_address LIMIT $3 OFFSET $4`, poolID, state, limit, offset)
	if err != nil {
		return ListResult[*models.DHCPLease]{}, fmt.Errorf("store: list leases in pool %s: %w", poolID, err)
	}
	defer rows.Close()

	items, err := collectLeases(rows)
	if err != nil {
		return ListResult[*models.DHCPLease]{}, fmt.Errorf("store: list leases in pool %s: %w", poolID, err)
	}
	return ListResult[*models.DHCPLease]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) GetLeaseByIPGlobal(ctx context.Context, ip net.IP) (*models.DHCPLease, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+leaseSelectCols+` FROM dhcp_leases
		WHERE ip_address = $1 AND binding_state = 'active' ORDER BY updated_at DESC LIMIT 1`, ip)
	l, err := scanLease(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get lease by ip %s: %w", ip, err)
	}
	return l, nil
}

func (p *Postgres) ListLeasesByMAC(ctx context.Context, mac string) ([]*models.DHCPLease, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil, fmt.Errorf("store: list leases by mac: invalid MAC %q: %w", mac, models.ErrInvalidInput)
	}
	rows, err := p.pool.Query(ctx, `SELECT `+leaseSelectCols+` FROM dhcp_leases
		WHERE client_mac = $1 ORDER BY updated_at DESC`, hw)
	if err != nil {
		return nil, fmt.Errorf("store: list leases by mac %s: %w", mac, err)
	}
	defer rows.Close()
	items, err := collectLeases(rows)
	if err != nil {
		return nil, fmt.Errorf("store: list leases by mac %s: %w", mac, err)
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// AllocationHelperStore.NextAvailableIP
// ---------------------------------------------------------------------------

func (p *Postgres) NextAvailableIP(ctx context.Context, poolID string) (net.IP, error) {
	candidates, err := p.scanCandidateIPs(ctx, poolID, 1)
	if err != nil {
		return nil, fmt.Errorf("store: next available ip in pool %s: %w", poolID, err)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("store: next available ip in pool %s: %w", poolID, models.ErrNoLeases)
	}
	return candidates[0], nil
}

// ---------------------------------------------------------------------------
// ReservationStore
// ---------------------------------------------------------------------------

const reservationSelectCols = `id, pool_id, hostname, description, reserved_ip, binding_key, binding_type, is_enabled,
	created_at, updated_at, created_by_user_id, updated_by_user_id`

func scanReservation(row pgx.Row) (*models.DHCPReservation, error) {
	var r models.DHCPReservation
	if err := row.Scan(&r.ID, &r.PoolID, &r.Hostname, &r.Description, &r.ReservedIP, &r.BindingKey, &r.BindingType,
		&r.IsEnabled, &r.CreatedAt, &r.UpdatedAt, &r.CreatedByUserID, &r.UpdatedByUserID); err != nil {
		return nil, err
	}
	return &r, nil
}

func (p *Postgres) ListReservationsByPool(ctx context.Context, poolID string, params ListParams) (ListResult[*models.DHCPReservation], error) {
	limit, offset := normalizeListParams(params)

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_reservations WHERE pool_id = $1`, poolID).Scan(&total); err != nil {
		return ListResult[*models.DHCPReservation]{}, fmt.Errorf("store: count reservations in pool %s: %w", poolID, err)
	}

	rows, err := p.pool.Query(ctx, `SELECT `+reservationSelectCols+` FROM dhcp_reservations
		WHERE pool_id = $1 ORDER BY reserved_ip LIMIT $2 OFFSET $3`, poolID, limit, offset)
	if err != nil {
		return ListResult[*models.DHCPReservation]{}, fmt.Errorf("store: list reservations in pool %s: %w", poolID, err)
	}
	defer rows.Close()

	var items []*models.DHCPReservation
	for rows.Next() {
		r, err := scanReservation(rows)
		if err != nil {
			return ListResult[*models.DHCPReservation]{}, fmt.Errorf("store: list reservations in pool %s: %w", poolID, err)
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.DHCPReservation]{}, fmt.Errorf("store: list reservations in pool %s: %w", poolID, err)
	}
	return ListResult[*models.DHCPReservation]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// rejectConflictingActiveLease returns models.ErrConflict when ip already
// has an active lease in poolID that belongs to a different client than
// bindingKey identifies. tryAllocateRound's reservation-match step (amendment
// A6, step 1) unconditionally wins the allocation round with no fallback to
// renew-affinity/candidate-scan, so a reservation created for a different
// client on an already actively-leased IP would silently starve that
// client's every DHCP request (ErrNoLeases forever, or until the other
// client's lease naturally expires). A reservation for the same client that
// already holds the lease (renew-in-place) is not a conflict.
func (p *Postgres) rejectConflictingActiveLease(ctx context.Context, poolID string, ip net.IP, bindingKey string) error {
	var mac net.HardwareAddr
	var clientID []byte
	var hostname *string
	err := p.pool.QueryRow(ctx, `
		SELECT client_mac, client_id, client_hostname FROM dhcp_leases
		WHERE pool_id = $1 AND ip_address = $2 AND binding_state = 'active'`,
		poolID, ip,
	).Scan(&mac, &clientID, &hostname)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: create reservation: check active lease on %s: %w", ip, err)
	}
	for _, key := range leaseIdentity(mac.String(), encodeClientID(clientID), hostname).BindingKeys() {
		if key == bindingKey {
			return nil
		}
	}
	return fmt.Errorf("store: create reservation: ip %s in pool %s already has an active lease for a different client: %w", ip, poolID, models.ErrConflict)
}

func (p *Postgres) GetReservation(ctx context.Context, id string) (*models.DHCPReservation, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+reservationSelectCols+` FROM dhcp_reservations WHERE id = $1`, id)
	r, err := scanReservation(row)
	if err != nil {
		return nil, mapReadError(err, "get reservation "+id)
	}
	return r, nil
}

func (p *Postgres) CreateReservation(ctx context.Context, r *models.DHCPReservation) error {
	if !r.BindingType.Valid() {
		return fmt.Errorf("store: create reservation: invalid binding_type %q: %w", r.BindingType, models.ErrInvalidInput)
	}
	// Validate binding_key is canonical form (type:value).
	if !models.IsCanonicalBindingKey(r.BindingKey) {
		return fmt.Errorf("store: create reservation: non-canonical binding_key %q (must be 'type:value'): %w", r.BindingKey, models.ErrInvalidInput)
	}
	pool, err := p.GetPoolByID(ctx, r.PoolID)
	if err != nil {
		return fmt.Errorf("store: create reservation: load pool %s: %w", r.PoolID, err)
	}
	if !ipInRange(r.ReservedIP, pool.RangeStart, pool.RangeEnd) {
		return fmt.Errorf("store: create reservation: reserved_ip %s outside pool %s range: %w", r.ReservedIP, r.PoolID, models.ErrInvalidInput)
	}
	if err := p.rejectConflictingActiveLease(ctx, r.PoolID, r.ReservedIP, r.BindingKey); err != nil {
		return err
	}
	if r.ID == "" {
		r.ID = newUUID()
	}

	err = p.pool.QueryRow(ctx, `
		INSERT INTO dhcp_reservations (id, pool_id, hostname, description, reserved_ip, binding_key, binding_type, is_enabled, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		RETURNING created_at, updated_at`,
		r.ID, r.PoolID, r.Hostname, r.Description, r.ReservedIP, r.BindingKey, r.BindingType, r.IsEnabled, r.CreatedByUserID,
	).Scan(&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create reservation")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) UpdateReservation(ctx context.Context, r *models.DHCPReservation) error {
	if !r.BindingType.Valid() {
		return fmt.Errorf("store: update reservation: invalid binding_type %q: %w", r.BindingType, models.ErrInvalidInput)
	}
	// Validate binding_key is canonical form (type:value).
	if !models.IsCanonicalBindingKey(r.BindingKey) {
		return fmt.Errorf("store: update reservation: non-canonical binding_key %q (must be 'type:value'): %w", r.BindingKey, models.ErrInvalidInput)
	}
	// An update can move the reserved IP or rebind it to a different client,
	// so it needs the same guards as a create: the address must stay inside
	// the pool, and must not already be leased to somebody else.
	pool, err := p.GetPoolByID(ctx, r.PoolID)
	if err != nil {
		return fmt.Errorf("store: update reservation: load pool %s: %w", r.PoolID, err)
	}
	if !ipInRange(r.ReservedIP, pool.RangeStart, pool.RangeEnd) {
		return fmt.Errorf("store: update reservation: reserved_ip %s outside pool %s range: %w", r.ReservedIP, r.PoolID, models.ErrInvalidInput)
	}
	if err := p.rejectConflictingActiveLease(ctx, r.PoolID, r.ReservedIP, r.BindingKey); err != nil {
		return err
	}
	err = p.pool.QueryRow(ctx, `
		UPDATE dhcp_reservations SET hostname=$2, description=$3, reserved_ip=$4, binding_key=$5, binding_type=$6,
			is_enabled=$7, updated_by_user_id=$8, updated_at=now()
		WHERE id = $1
		RETURNING updated_at`,
		r.ID, r.Hostname, r.Description, r.ReservedIP, r.BindingKey, r.BindingType, r.IsEnabled, r.UpdatedByUserID,
	).Scan(&r.UpdatedAt)
	if err != nil {
		return mapWriteError(mapReadError(err, "update reservation "+r.ID), "update reservation")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) DeleteReservation(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM dhcp_reservations WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete reservation")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete reservation %s: %w", id, models.ErrNotFound)
	}
	p.notifyConfigChanged(ctx)
	return nil
}

// ipInRange reports whether ip falls within [start, end], inclusive,
// comparing raw address bytes (both operands normalized to 16-byte form so
// IPv4-mapped and native representations compare equal).
func ipInRange(ip, start, end net.IP) bool {
	i, s, e := ip.To16(), start.To16(), end.To16()
	if i == nil || s == nil || e == nil {
		return false
	}
	return compareIP(i, s) >= 0 && compareIP(i, e) <= 0
}

func compareIP(a, b net.IP) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// logStatsWarn logs a failure to update the stats_active_lease_count
// bookkeeping cache. It never fails the caller: the counter is a fast-path
// cache reconciled by SampleUtilization, not the source of truth for
// utilization (which always recomputes from dhcp_leases directly).
func logStatsWarn(op string, err error) {
	slog.Default().Warn("store: stats bookkeeping failed", "op", op, "error", err)
}
