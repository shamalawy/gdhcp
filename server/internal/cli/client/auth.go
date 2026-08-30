package client

import (
	"context"
	"time"
)

// LoginResponse is the response shape for POST /auth/login and
// POST /auth/refresh (docs/api/endpoints.md).
type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Login exchanges username+password for a session JWT (24h TTL per
// docs/api/endpoints.md). It does not require an existing token.
func (c *Client) Login(ctx context.Context, username, password string) (*LoginResponse, error) {
	req := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: username, Password: password}

	var resp LoginResponse
	if err := c.post(ctx, "/api/v1/auth/login", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Logout revokes the current session token.
func (c *Client) Logout(ctx context.Context) error {
	return c.post(ctx, "/api/v1/auth/logout", nil, nil)
}

// Refresh exchanges a refresh token for a new session JWT.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*LoginResponse, error) {
	req := struct {
		RefreshToken string `json:"refresh_token"`
	}{RefreshToken: refreshToken}

	var resp LoginResponse
	if err := c.post(ctx, "/api/v1/auth/refresh", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
