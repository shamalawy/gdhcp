package client

import (
	"context"
	"fmt"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// VRFCreateRequest is the request body for POST /vrfs.
type VRFCreateRequest struct {
	Name               string  `json:"name"`
	Description        *string `json:"description,omitempty"`
	RouteDistinguisher *string `json:"route_distinguisher,omitempty"`
}

// VRFUpdateRequest is the request body for PUT /vrfs/{vrf_id}.
type VRFUpdateRequest struct {
	Name               *string `json:"name,omitempty"`
	Description        *string `json:"description,omitempty"`
	RouteDistinguisher *string `json:"route_distinguisher,omitempty"`
}

// ListVRFs returns GET /vrfs.
func (c *Client) ListVRFs(ctx context.Context, p ListParams) (*Page[models.VRF], error) {
	var page Page[models.VRF]
	if err := c.get(ctx, "/api/v1/vrfs", p.Values(), &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetVRF returns GET /vrfs/{vrf_id}.
func (c *Client) GetVRF(ctx context.Context, id string) (*models.VRF, error) {
	var v models.VRF
	if err := c.get(ctx, "/api/v1/vrfs/"+id, nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// CreateVRF returns POST /vrfs.
func (c *Client) CreateVRF(ctx context.Context, req VRFCreateRequest) (*models.VRF, error) {
	var v models.VRF
	if err := c.post(ctx, "/api/v1/vrfs", req, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// UpdateVRF returns PUT /vrfs/{vrf_id}.
func (c *Client) UpdateVRF(ctx context.Context, id string, req VRFUpdateRequest) (*models.VRF, error) {
	var v models.VRF
	if err := c.put(ctx, "/api/v1/vrfs/"+id, req, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// DeleteVRF returns DELETE /vrfs/{vrf_id}.
func (c *Client) DeleteVRF(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/vrfs/%s", id))
}
