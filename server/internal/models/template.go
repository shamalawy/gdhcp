package models

import "time"

// Template represents a reusable bundle of DHCP option values
// (dhcp_templates table) that can be attached to multiple scopes/pools.
type Template struct {
	ID              string    `json:"id" db:"id"`
	Name            string    `json:"name" db:"name"`
	Description     *string   `json:"description" db:"description"`
	IsEnabled       bool      `json:"is_enabled" db:"is_enabled"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
	CreatedByUserID *int64    `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID *int64    `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
}

// TemplateScopeAssoc represents a template's association to a scope
// (dhcp_template_scopes table). Priority orders multiple templates applied
// to the same scope (higher = checked first in option inheritance).
type TemplateScopeAssoc struct {
	ID         string    `json:"id" db:"id"`
	TemplateID string    `json:"template_id" db:"template_id"`
	ScopeID    string    `json:"scope_id" db:"scope_id"`
	Priority   int       `json:"priority" db:"priority"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

// TemplatePoolAssoc represents a template's association to a pool
// (dhcp_template_pools table).
type TemplatePoolAssoc struct {
	ID         string    `json:"id" db:"id"`
	TemplateID string    `json:"template_id" db:"template_id"`
	PoolID     string    `json:"pool_id" db:"pool_id"`
	Priority   int       `json:"priority" db:"priority"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}
