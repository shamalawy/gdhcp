package client

import (
	"context"
	"fmt"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// PoolCreateRequest is the request body for POST /scopes/{scope_id}/pools.
type PoolCreateRequest struct {
	Name       string `json:"name"`
	RangeStart string `json:"range_start"`
	RangeEnd   string `json:"range_end"`
	Priority   int    `json:"priority"`
}

// PoolUpdateRequest is the request body for PUT /pools/{pool_id}.
type PoolUpdateRequest struct {
	Name       *string `json:"name,omitempty"`
	RangeStart *string `json:"range_start,omitempty"`
	RangeEnd   *string `json:"range_end,omitempty"`
	Priority   *int    `json:"priority,omitempty"`
	IsEnabled  *bool   `json:"is_enabled,omitempty"`
}

// ListPools returns GET /scopes/{scope_id}/pools.
func (c *Client) ListPools(ctx context.Context, scopeID string, p ListParams) (*Page[models.DHCPPool], error) {
	var page Page[models.DHCPPool]
	if err := c.get(ctx, fmt.Sprintf("/api/v1/scopes/%s/pools", scopeID), p.Values(), &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetPool returns GET /pools/{pool_id}.
func (c *Client) GetPool(ctx context.Context, id string) (*models.DHCPPool, error) {
	var pool models.DHCPPool
	if err := c.get(ctx, "/api/v1/pools/"+id, nil, &pool); err != nil {
		return nil, err
	}
	return &pool, nil
}

// CreatePool returns POST /scopes/{scope_id}/pools.
func (c *Client) CreatePool(ctx context.Context, scopeID string, req PoolCreateRequest) (*models.DHCPPool, error) {
	var pool models.DHCPPool
	if err := c.post(ctx, fmt.Sprintf("/api/v1/scopes/%s/pools", scopeID), req, &pool); err != nil {
		return nil, err
	}
	return &pool, nil
}

// UpdatePool returns PUT /pools/{pool_id}.
func (c *Client) UpdatePool(ctx context.Context, id string, req PoolUpdateRequest) (*models.DHCPPool, error) {
	var pool models.DHCPPool
	if err := c.put(ctx, "/api/v1/pools/"+id, req, &pool); err != nil {
		return nil, err
	}
	return &pool, nil
}

// DeletePool returns DELETE /pools/{pool_id}.
func (c *Client) DeletePool(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/pools/%s", id))
}

// PoolUtilization returns GET /pools/{pool_id}/utilization.
func (c *Client) PoolUtilization(ctx context.Context, id string) (*models.Utilization, error) {
	var u models.Utilization
	if err := c.get(ctx, fmt.Sprintf("/api/v1/pools/%s/utilization", id), nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// PoolLeases returns GET /pools/{pool_id}/leases, optionally filtered by
// state (empty means all states).
func (c *Client) PoolLeases(ctx context.Context, poolID string, p ListParams, state string) (*Page[models.DHCPLease], error) {
	q := p.Values()
	if state != "" {
		q.Set("state", state)
	}
	var page Page[models.DHCPLease]
	if err := c.get(ctx, fmt.Sprintf("/api/v1/pools/%s/leases", poolID), q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// ReservationCreateRequest is the request body for
// POST /pools/{pool_id}/reservations.
type ReservationCreateRequest struct {
	BindingKey  string  `json:"binding_key"`
	BindingType string  `json:"binding_type"`
	ReservedIP  string  `json:"reserved_ip"`
	Hostname    *string `json:"hostname,omitempty"`
	Description *string `json:"description,omitempty"`
}

// ReservationUpdateRequest is the request body for
// PUT /reservations/{reservation_id}.
type ReservationUpdateRequest struct {
	Hostname    *string `json:"hostname,omitempty"`
	Description *string `json:"description,omitempty"`
	IsEnabled   *bool   `json:"is_enabled,omitempty"`
	BindingKey  *string `json:"binding_key,omitempty"`
	BindingType *string `json:"binding_type,omitempty"`
}

// ListReservations returns GET /pools/{pool_id}/reservations.
func (c *Client) ListReservations(ctx context.Context, poolID string, p ListParams) (*Page[models.DHCPReservation], error) {
	var page Page[models.DHCPReservation]
	if err := c.get(ctx, fmt.Sprintf("/api/v1/pools/%s/reservations", poolID), p.Values(), &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetReservation returns GET /reservations/{reservation_id}.
func (c *Client) GetReservation(ctx context.Context, id string) (*models.DHCPReservation, error) {
	var r models.DHCPReservation
	if err := c.get(ctx, "/api/v1/reservations/"+id, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// AddReservation returns POST /pools/{pool_id}/reservations.
func (c *Client) AddReservation(ctx context.Context, poolID string, req ReservationCreateRequest) (*models.DHCPReservation, error) {
	var r models.DHCPReservation
	if err := c.post(ctx, fmt.Sprintf("/api/v1/pools/%s/reservations", poolID), req, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateReservation returns PUT /reservations/{reservation_id}.
func (c *Client) UpdateReservation(ctx context.Context, id string, req ReservationUpdateRequest) (*models.DHCPReservation, error) {
	var r models.DHCPReservation
	if err := c.put(ctx, "/api/v1/reservations/"+id, req, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// RemoveReservation returns DELETE /reservations/{reservation_id}.
func (c *Client) RemoveReservation(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/reservations/%s", id))
}
