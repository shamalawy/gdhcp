package models

import "time"

// PrefixType enumerates the levels of the IPAM hierarchy.
type PrefixType string

const (
	PrefixTypeAggregate PrefixType = "aggregate"
	PrefixTypeContainer PrefixType = "container"
	PrefixTypeSubnet    PrefixType = "subnet"
)

// Valid reports whether t is a known prefix type.
func (t PrefixType) Valid() bool {
	switch t {
	case PrefixTypeAggregate, PrefixTypeContainer, PrefixTypeSubnet:
		return true
	default:
		return false
	}
}

// IPAMPrefix represents a node in the IPAM prefix hierarchy (ipam_prefixes
// table): an aggregate, a container, or an allocatable subnet.
type IPAMPrefix struct {
	ID                    string     `json:"id" db:"id"`
	VRFID                 string     `json:"vrf_id" db:"vrf_id"`
	Prefix                *IPNet     `json:"prefix" db:"prefix"`
	ParentID              *string    `json:"parent_id" db:"parent_id"`
	PrefixType            PrefixType `json:"prefix_type" db:"prefix_type"`
	Name                  *string    `json:"name" db:"name"`
	Description           *string    `json:"description" db:"description"`
	UtilizationPercentage *int       `json:"utilization_percentage" db:"utilization_percentage"`
	VLANID                *string    `json:"vlan_id" db:"vlan_id"`
	// VLANVID and VLANName are read-only joins so a prefix row can show its
	// VLAN without a second request. They are never written back.
	VLANVID         *int      `json:"vlan_vid,omitempty" db:"-"`
	VLANName        *string   `json:"vlan_name,omitempty" db:"-"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
	CreatedByUserID *int64    `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID *int64    `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
}

// IPAMTreeNode is a lazily-expandable node returned by GET /api/v1/ipam/tree.
// It carries the prefix itself plus enough summary data for the frontend
// tree view to render without a further round trip, and Children is
// populated only to the requested depth.
type IPAMTreeNode struct {
	Prefix     *IPAMPrefix `json:"prefix"`
	ChildCount int         `json:"child_count"`
	HasScope   bool        `json:"has_scope"`
	ScopeID    *string     `json:"scope_id,omitempty"`
	// SpacePercentage is how much of this prefix's CIDR its direct children
	// cover. Unlike Prefix.UtilizationPercentage — DHCP lease usage, which is
	// NULL wherever no scope exists beneath the node — this is defined for
	// every node, so aggregates and containers in a pure-IPAM deployment
	// still have something to render a bar from.
	SpacePercentage float64         `json:"space_pct"`
	Children        []*IPAMTreeNode `json:"children,omitempty"`
	ChildrenMore    bool            `json:"children_more"`
}
