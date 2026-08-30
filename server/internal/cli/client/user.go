package client

import (
	"context"
	"fmt"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// UserCreateRequest is the request body for POST /users.
type UserCreateRequest struct {
	Username    string  `json:"username"`
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	Description *string `json:"description,omitempty"`
}

// UserUpdateRequest is the request body for PUT /users/{user_id}.
type UserUpdateRequest struct {
	Username    *string `json:"username,omitempty"`
	Email       *string `json:"email,omitempty"`
	Description *string `json:"description,omitempty"`
	IsActive    *bool   `json:"is_active,omitempty"`
}

// TokenCreateRequest is the request body for POST /users/{user_id}/tokens.
type TokenCreateRequest struct {
	Description string     `json:"description"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// ListUsers returns GET /users (admin only).
func (c *Client) ListUsers(ctx context.Context, p ListParams) (*Page[models.User], error) {
	var page Page[models.User]
	if err := c.get(ctx, "/api/v1/users", p.Values(), &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetUser returns GET /users/{user_id}.
func (c *Client) GetUser(ctx context.Context, id string) (*models.User, error) {
	var u models.User
	if err := c.get(ctx, "/api/v1/users/"+id, nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser returns POST /users (admin only).
func (c *Client) CreateUser(ctx context.Context, req UserCreateRequest) (*models.User, error) {
	var u models.User
	if err := c.post(ctx, "/api/v1/users", req, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdateUser returns PUT /users/{user_id} (admin only).
func (c *Client) UpdateUser(ctx context.Context, id string, req UserUpdateRequest) (*models.User, error) {
	var u models.User
	if err := c.put(ctx, "/api/v1/users/"+id, req, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteUser returns DELETE /users/{user_id} (admin only).
func (c *Client) DeleteUser(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/users/%s", id))
}

// ListUserTokens returns GET /users/{user_id}/tokens.
func (c *Client) ListUserTokens(ctx context.Context, userID string, p ListParams) ([]*models.APIToken, error) {
	var tokens []*models.APIToken
	if err := c.get(ctx, fmt.Sprintf("/api/v1/users/%s/tokens", userID), p.Values(), &tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

// CreateUserToken returns POST /users/{user_id}/tokens. The raw token in
// the response is shown exactly once (amendment A14) — callers must
// display and never persist it beyond the immediate command output, unless
// this call itself is `gdhcp login` populating the client config file.
func (c *Client) CreateUserToken(ctx context.Context, userID string, req TokenCreateRequest) (*models.APITokenCreated, error) {
	var tok models.APITokenCreated
	if err := c.post(ctx, fmt.Sprintf("/api/v1/users/%s/tokens", userID), req, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// RevokeUserToken returns DELETE /users/{user_id}/tokens/{token_id}.
func (c *Client) RevokeUserToken(ctx context.Context, userID, tokenID string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/users/%s/tokens/%s", userID, tokenID))
}
