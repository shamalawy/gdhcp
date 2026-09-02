package models

import (
	"net"
	"time"
)

// DHCPScope represents a DHCP-served subnet (dhcp_scopes table), bound to a
// VRF, an HA cluster, and the IPAM prefix it was carved from.
type DHCPScope struct {
	ID              string    `json:"id" db:"id"`
	VRFID           string    `json:"vrf_id" db:"vrf_id"`
	ClusterID       string    `json:"cluster_id" db:"cluster_id"`
	IPAMPrefixID    string    `json:"ipam_prefix_id" db:"ipam_prefix_id"`
	Name            string    `json:"name" db:"name"`
	Description     *string   `json:"description" db:"description"`
	Network         *IPNet    `json:"network" db:"network"` // Cached from ipam_prefix
	IsEnabled       bool      `json:"is_enabled" db:"is_enabled"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
	CreatedByUserID *int64    `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID *int64    `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
}

// DHCPPool represents a contiguous allocatable IP range within a scope
// (dhcp_pools table). Priority orders allocation preference among pools in
// the same scope (higher = preferred).
type DHCPPool struct {
	ID              string    `json:"id" db:"id"`
	ScopeID         string    `json:"scope_id" db:"scope_id"`
	Name            string    `json:"name" db:"name"`
	Description     *string   `json:"description" db:"description"`
	RangeStart      net.IP    `json:"range_start" db:"range_start"`
	RangeEnd        net.IP    `json:"range_end" db:"range_end"`
	IsEnabled       bool      `json:"is_enabled" db:"is_enabled"`
	Priority        int       `json:"priority" db:"priority"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
	CreatedByUserID *int64    `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID *int64    `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
}

// DHCPReservation represents a fixed IP-to-client binding within a pool
// (dhcp_reservations table). BindingKey is ALWAYS stored in the canonical
// type-prefixed form "<binding_type>:<canonical value>" (e.g.
// "mac:aa:bb:cc:dd:ee:ff") that the allocation algorithm (amendment A6)
// matches against incoming requests -- see models.BuildBindingKey, which every
// write path must go through. BindingType and BindingKey's prefix always
// agree.
type DHCPReservation struct {
	ID              string      `json:"id" db:"id"`
	PoolID          string      `json:"pool_id" db:"pool_id"`
	Hostname        *string     `json:"hostname" db:"hostname"`
	Description     *string     `json:"description" db:"description"`
	ReservedIP      net.IP      `json:"reserved_ip" db:"reserved_ip"`
	BindingKey      string      `json:"binding_key" db:"binding_key"`
	BindingType     BindingType `json:"binding_type" db:"binding_type"`
	IsEnabled       bool        `json:"is_enabled" db:"is_enabled"`
	CreatedAt       time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at" db:"updated_at"`
	CreatedByUserID *int64      `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID *int64      `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
}

// LeaseBindingState enumerates the lifecycle states of a DHCPLease. These are
// DHCP protocol / row-provenance facts, NOT an address status: three of the
// four mean the same thing to the allocator. Use AddressStatus for anything an
// operator reads -- amendment A20.
type LeaseBindingState string

const (
	// LeaseStateActive: a client holds this address. scanCandidateIPs and
	// CanAllocateInPool both exclude it, and claimLeaseIP will only reclaim it
	// for the same client (renew-in-place affinity).
	LeaseStateActive LeaseBindingState = "active"

	// LeaseStateExpired: the reaper found lease_end in the past. The address is
	// free -- claimLeaseIP's reclaim clause accepts it.
	LeaseStateExpired LeaseBindingState = "expired"

	// LeaseStateReleased: the client sent RELEASE, or an operator released
	// it. DECLINE lands in abandoned instead, per RFC 2131 §4.3.3 -- see
	// LeaseStateAbandoned. Operationally IDENTICAL to expired: claimLeaseIP matches
	// both with one `binding_state IN ('expired', 'released')` clause. The two
	// differ only in how the row got here.
	LeaseStateReleased LeaseBindingState = "released"

	// LeaseStateAbandoned: held back after a conflict. The only state that is
	// genuinely distinct -- claimLeaseIP's reclaim clause omits it, so the
	// address stays unclaimable until ExpireLeasesOlderThan sweeps it past
	// lease_end and rewrites it to expired.
	LeaseStateAbandoned LeaseBindingState = "abandoned"
)

// Valid reports whether s is a known binding state.
func (s LeaseBindingState) Valid() bool {
	switch s {
	case LeaseStateActive, LeaseStateExpired, LeaseStateReleased, LeaseStateAbandoned:
		return true
	default:
		return false
	}
}

// DHCPLease represents an allocated DHCP lease (dhcp_leases table).
// BindingServerID is nullable (ON DELETE SET NULL per amendment A5.1): a
// lease can outlive the server that issued it.
type DHCPLease struct {
	ID              string            `json:"id" db:"id"`
	PoolID          string            `json:"pool_id" db:"pool_id"`
	IPAddress       net.IP            `json:"ip_address" db:"ip_address"`
	ClientMAC       *HardwareAddr     `json:"client_mac" db:"client_mac"`
	ClientID        []byte            `json:"client_id,omitempty" db:"client_id"`
	ClientHostname  *string           `json:"client_hostname" db:"client_hostname"`
	BindingState    LeaseBindingState `json:"binding_state" db:"binding_state"`
	BindingServerID *string           `json:"binding_server_id" db:"binding_server_id"`
	LeaseStart      time.Time         `json:"lease_start" db:"lease_start"`
	LeaseEnd        time.Time         `json:"lease_end" db:"lease_end"`
	RenewalTime     *time.Time        `json:"renewal_time" db:"renewal_time"`
	RebindingTime   *time.Time        `json:"rebinding_time" db:"rebinding_time"`
	CreatedAt       time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at" db:"updated_at"`

	// ReservationState and Reservation are amendment A19: they are derived on
	// read from dhcp_reservations, never stored on the lease row. Only the
	// API-facing read paths populate them; the DHCP engine's own hot-path
	// lookups leave ReservationState empty, and those leases never reach JSON.
	ReservationState LeaseReservationState `json:"reservation_state" db:"-"`
	Reservation      *LeaseReservation     `json:"reservation" db:"-"`

	// AddressStatus is amendment A20: the one status an operator reads, derived
	// from BindingState and Reservation together. Populated by the same read
	// paths that populate ReservationState.
	AddressStatus AddressStatus `json:"address_status" db:"-"`
}

// LeaseReservationState names how a lease relates to the reservations in its
// pool. It exists because "is this address reserved?" and "is this client
// reserved?" are different questions, and an operator who cannot tell them
// apart will keep trying to pin a client that is already pinned -- which
// UNIQUE (pool_id, binding_key) rejects.
type LeaseReservationState string

const (
	// LeaseNotPinned: no reservation in the pool matches this lease's client
	// or its address. Pinning is safe.
	LeaseNotPinned LeaseReservationState = "not_pinned"

	// LeasePinned: an enabled reservation binds this lease's client to this
	// exact address. The address the client holds is the address it is
	// guaranteed to keep.
	LeasePinned LeaseReservationState = "pinned"

	// LeasePinnedDisabled: the same binding exists but the reservation is
	// disabled. reservationForKeys and scanCandidateIPs both filter on
	// is_enabled, so a disabled reservation is invisible to allocation: it
	// neither holds the address nor blocks anyone else from taking it.
	LeasePinnedDisabled LeaseReservationState = "pinned_disabled"

	// LeasePinnedElsewhere: this client is pinned, but to a DIFFERENT
	// address. Creating a second reservation for it would violate
	// UNIQUE (pool_id, binding_key) -- the existing reservation has to be
	// moved, not added to. This is the state that produced the raw
	// "duplicate key value violates unique constraint" error operators saw.
	LeasePinnedElsewhere LeaseReservationState = "pinned_elsewhere"

	// LeaseAddressReserved: this address is reserved for a different client.
	// The current holder keeps it until its lease lapses, at which point the
	// reserved client can claim it.
	LeaseAddressReserved LeaseReservationState = "address_reserved"
)

// LeaseReservation is the one reservation that concerns a lease: the
// reservation matching the lease's client, or failing that the reservation
// on the lease's address. It is a projection of dhcp_reservations, not the
// full row -- a lease row only needs enough to explain its state and to let
// the UI act on the reservation (unpin, or move the pin here).
type LeaseReservation struct {
	ID          string      `json:"id"`
	ReservedIP  net.IP      `json:"reserved_ip"`
	BindingKey  string      `json:"binding_key"`
	BindingType BindingType `json:"binding_type"`
	IsEnabled   bool        `json:"is_enabled"`

	// MatchesClient reports that BindingKey is one of the lease client's own
	// candidate keys (see ClientIdentity.BindingKeys).
	MatchesClient bool `json:"matches_client"`
	// MatchesAddress reports that ReservedIP is the lease's address.
	MatchesAddress bool `json:"matches_address"`
}

// ClassifyLeaseReservation derives a lease's LeaseReservationState from the
// reservation found for it, if any. Postgres and the in-memory mock both
// call this, so the two can never disagree about what "pinned" means.
func ClassifyLeaseReservation(res *LeaseReservation) LeaseReservationState {
	switch {
	case res == nil:
		return LeaseNotPinned
	case res.MatchesClient && res.MatchesAddress && res.IsEnabled:
		return LeasePinned
	case res.MatchesClient && res.MatchesAddress:
		return LeasePinnedDisabled
	case res.MatchesClient:
		return LeasePinnedElsewhere
	default:
		return LeaseAddressReserved
	}
}

// AddressStatus is what an address is actually doing, and the only status
// vocabulary the UI and CLI are allowed to show. It exists because
// LeaseBindingState is a protocol fact rather than a status: `released` and
// `expired` both mean the address is free, and rendering them as distinct
// coloured chips told operators an address was in some special state when the
// allocator had already handed it back to the pool.
//
// The four values partition an address exactly the way the utilization rollup
// does (see toUtilization's used / reserved / free), so a prefix's counts and
// its client table can never contradict each other -- which they did before
// amendment A20.
type AddressStatus string

const (
	// AddressFree: nothing holds this address. No lease, or a lease that is
	// released or expired -- claimLeaseIP reclaims both without ceremony.
	AddressFree AddressStatus = "free"

	// AddressAssigned: a client currently holds this address (an active
	// lease). Excluded from scanCandidateIPs, counted as used.
	AddressAssigned AddressStatus = "assigned"

	// AddressReserved: an enabled reservation holds this address for a client
	// that is not currently on it. The address is kept out of the dynamic
	// range waiting for that client -- this is scanCandidateIPs' reservation
	// exclusion, and the utilization rollup's reservedUnleased.
	AddressReserved AddressStatus = "reserved"

	// AddressAbandoned: held back after a conflict. Distinct from free because
	// claimLeaseIP genuinely refuses to reclaim it until the reaper sweeps it.
	AddressAbandoned AddressStatus = "abandoned"
)

// ClassifyAddressStatus derives the operator-facing status of one address from
// its lease binding state and the reservation concerning it. state is nil when
// no lease row exists for the address at all.
//
// Precedence is deliberate:
//
//   - abandoned first: claimLeaseIP will not reclaim it, whatever else is true.
//   - then an enabled reservation. A reservation is a durable property of the
//     ADDRESS -- it is how the address is allocated, statically rather than
//     dynamically -- so it outranks whoever happens to be holding the address
//     at this instant. Reserving an address a client is currently using must
//     make the address read as Reserved immediately; anything else means an
//     operator reserves an address and the UI still calls it Assigned.
//     A DISABLED reservation deliberately does not count -- scanCandidateIPs
//     filters on is_enabled, so it holds nothing and the address really is free.
//   - then an active lease, i.e. a dynamically assigned address.
//   - otherwise free, which folds in released and expired.
//
// Assigned and Reserved are therefore ALLOCATION MODES (dynamic vs static),
// not lifecycle states, and they are mutually exclusive. Occupancy is still
// visible on the row itself -- the client MAC, hostname and lease expiry are
// columns of their own.
//
// Postgres and the in-memory mock both call this, so the two can never
// disagree, exactly as ClassifyLeaseReservation does for pin state.
func ClassifyAddressStatus(state *LeaseBindingState, res *LeaseReservation) AddressStatus {
	switch {
	case state != nil && *state == LeaseStateAbandoned:
		return AddressAbandoned
	case res != nil && res.IsEnabled:
		return AddressReserved
	case state != nil && *state == LeaseStateActive:
		return AddressAssigned
	default:
		return AddressFree
	}
}
