// Package authctx carries the authenticated caller (the "principal") on the
// request context. It is a leaf package (no dependency on package api or its
// resource sub-packages) so both can read/write the principal without an
// import cycle.
package authctx

import (
	"context"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// Principal identifies the authenticated caller of a request, resolved by
// the auth middleware from either a session JWT or an API token.
type Principal struct {
	UserID   int64
	Username string
	Role     models.Role

	// TokenID is set when the caller authenticated via an API token
	// (nil for session-JWT callers), for audit/debugging.
	TokenID *int64
}

type principalKeyType struct{}

var principalKey = principalKeyType{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// FromContext returns the request's authenticated principal, or nil if the
// request is unauthenticated (only true for the handful of routes auth
// middleware skips: /auth/login, /health).
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

// roleRank orders roles from least to most privileged so RBAC checks can
// compare with a single ">=".
var roleRank = map[models.Role]int{
	models.RoleViewer:   1,
	models.RoleOperator: 2,
	models.RoleAdmin:    3,
}

// Allows reports whether p's role meets or exceeds the required role.
func (p *Principal) Allows(required models.Role) bool {
	if p == nil {
		return false
	}
	return roleRank[p.Role] >= roleRank[required]
}
