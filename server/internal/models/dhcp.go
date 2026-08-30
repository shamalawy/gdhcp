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

// LeaseBindingState enumerates the lifecycle states of a DHCPLease.
type LeaseBindingState string

const (
	LeaseStateActive    LeaseBindingState = "active"
	LeaseStateExpired   LeaseBindingState = "expired"
	LeaseStateReleased  LeaseBindingState = "released"
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
}
