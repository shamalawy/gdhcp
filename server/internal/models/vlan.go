package models

import "time"

// VLANStatus enumerates the lifecycle states a VLAN can be in. Only
// 'active' VLANs are normally assigned to new prefixes; 'reserved' marks a
// VID held back for future use, 'deprecated' one being retired.
type VLANStatus string

const (
	VLANStatusActive     VLANStatus = "active"
	VLANStatusReserved   VLANStatus = "reserved"
	VLANStatusDeprecated VLANStatus = "deprecated"
)

// Valid reports whether s is a known VLAN status.
func (s VLANStatus) Valid() bool {
	switch s {
	case VLANStatusActive, VLANStatusReserved, VLANStatusDeprecated:
		return true
	default:
		return false
	}
}

// VLAN ID bounds per IEEE 802.1Q: 0 and 4095 are reserved.
const (
	MinVID = 1
	MaxVID = 4094
)

// VLANGroup scopes a set of VLAN IDs to one layer-2 domain (a site, a
// campus, a fabric), so the same VID can legitimately exist more than once
// across the estate. VLANs with no group share a single implicit global
// domain in which each VID is unique.
type VLANGroup struct {
	ID              string    `json:"id" db:"id"`
	Name            string    `json:"name" db:"name"`
	Description     *string   `json:"description" db:"description"`
	VRFID           *string   `json:"vrf_id" db:"vrf_id"`
	MinVID          int       `json:"min_vid" db:"min_vid"`
	MaxVID          int       `json:"max_vid" db:"max_vid"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
	CreatedByUserID *int64    `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID *int64    `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
}

// VLAN is one 802.1Q broadcast domain. IPAM prefixes reference it via
// ipam_prefixes.vlan_id to record which segment an address block serves.
type VLAN struct {
	ID          string     `json:"id" db:"id"`
	GroupID     *string    `json:"group_id" db:"group_id"`
	VID         int        `json:"vid" db:"vid"`
	Name        string     `json:"name" db:"name"`
	Description *string    `json:"description" db:"description"`
	Status      VLANStatus `json:"status" db:"status"`
	// GroupName and PrefixCount are read-only joins the list/detail
	// endpoints fill in so the UI can render a VLAN row without N+1
	// follow-up calls. They are never persisted.
	GroupName       *string   `json:"group_name,omitempty" db:"-"`
	PrefixCount     int       `json:"prefix_count" db:"-"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
	CreatedByUserID *int64    `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID *int64    `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
}
