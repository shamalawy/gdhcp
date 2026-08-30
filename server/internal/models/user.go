package models

import "time"

// Role is the RBAC role assigned to a user (and inherited by their API
// tokens). See amendment A15: admin has full access; operator has all CRUD
// except users/tokens/cluster config; viewer is read-only.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// Valid reports whether r is one of the known roles.
func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleOperator, RoleViewer:
		return true
	default:
		return false
	}
}

// User represents a system user (users table). IDs are BIGSERIAL, not UUIDs,
// per docs/db/schema.sql.
type User struct {
	ID            int64     `json:"id" db:"id"`
	Username      string    `json:"username" db:"username"`
	Email         string    `json:"email" db:"email"`
	PasswordHash  string    `json:"-" db:"password_hash"` // Never exposed in API
	Role          Role      `json:"role" db:"role"`
	Description   *string   `json:"description" db:"description"`
	IsActive      bool      `json:"is_active" db:"is_active"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
	CreatedByUser *User     `json:"created_by_user,omitempty" db:"-"` // Nested; omit in list responses
	UpdatedByUser *User     `json:"updated_by_user,omitempty" db:"-"`
}

// APIToken represents a long-lived API access token (api_tokens table).
// Per amendment A14, TokenHash stores the SHA-256 hex digest of the raw
// token (not bcrypt, which cannot support deterministic lookup); the raw
// token itself is returned exactly once at creation time and never stored.
type APIToken struct {
	ID          int64      `json:"id" db:"id"`
	UserID      int64      `json:"user_id" db:"user_id"`
	TokenHash   string     `json:"-" db:"token_hash"` // Never exposed; SHA-256 hex of raw token
	Description *string    `json:"description" db:"description"`
	LastUsedAt  *time.Time `json:"last_used_at" db:"last_used_at"`
	ExpiresAt   *time.Time `json:"expires_at" db:"expires_at"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// APITokenCreated is the one-time response returned from token creation,
// carrying the raw token value that will never be retrievable again.
type APITokenCreated struct {
	APIToken
	Token string `json:"token"`
}
