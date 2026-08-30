package models

import "time"

// AuditAction enumerates the kinds of action an AuditLog entry records.
type AuditAction string

const (
	AuditActionCreate       AuditAction = "create"
	AuditActionUpdate       AuditAction = "update"
	AuditActionDelete       AuditAction = "delete"
	AuditActionLeaseAssign  AuditAction = "lease_assign"
	AuditActionLeaseRelease AuditAction = "lease_release"
	AuditActionLogin        AuditAction = "login"
	AuditActionFailover     AuditAction = "failover"
)

// AuditChange captures the before/after of one changed field, keyed by
// field name in AuditLog.Changes.
type AuditChange struct {
	Old any `json:"old"`
	New any `json:"new"`
}

// AuditLog represents one compliance/debugging audit trail entry
// (audit_log table).
type AuditLog struct {
	ID          int64                  `json:"id" db:"id"`
	UserID      *int64                 `json:"user_id" db:"user_id"`
	Action      AuditAction            `json:"action" db:"action"`
	EntityType  string                 `json:"entity_type" db:"entity_type"`
	EntityID    string                 `json:"entity_id" db:"entity_id"`
	EntityName  *string                `json:"entity_name" db:"entity_name"`
	Changes     map[string]AuditChange `json:"changes,omitempty" db:"changes"`
	IPAddress   *string                `json:"ip_address" db:"ip_address"`
	UserAgent   *string                `json:"user_agent" db:"user_agent"`
	Timestamp   time.Time              `json:"timestamp" db:"timestamp"`
	Description *string                `json:"description" db:"description"`

	// User is populated by list queries that join the acting user's
	// username so audit views don't need a second lookup per row.
	User *User `json:"user,omitempty" db:"-"`
}
