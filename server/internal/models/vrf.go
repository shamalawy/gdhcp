package models

import "time"

// VRF represents a virtual routing/forwarding domain (vrfs table). VRFs
// scope IPAM prefixes and DHCP scopes so overlapping address space can
// coexist across routing domains.
type VRF struct {
	ID                 string    `json:"id" db:"id"`
	Name               string    `json:"name" db:"name"`
	RouteDistinguisher *string   `json:"route_distinguisher" db:"route_distinguisher"`
	Description        *string   `json:"description" db:"description"`
	IsDefault          bool      `json:"is_default" db:"is_default"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time `json:"updated_at" db:"updated_at"`
	CreatedByUserID    *int64    `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID    *int64    `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
}
