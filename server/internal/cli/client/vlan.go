package client

import (
	"context"
	"fmt"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// VLANCreateRequest is the request body for POST /vlans.
type VLANCreateRequest struct {
	VID         int     `json:"vid"`
	Name        string  `json:"name"`
	GroupID     *string `json:"group_id,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      string  `json:"status,omitempty"`
}

// VLANUpdateRequest is the request body for PUT /vlans/{vlan_id}.
type VLANUpdateRequest struct {
	VID         *int    `json:"vid,omitempty"`
	Name        string  `json:"name"`
	GroupID     *string `json:"group_id,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      string  `json:"status,omitempty"`
}

// VLANGroupRequest is the request body for POST and PUT /vlan-groups.
type VLANGroupRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	VRFID       *string `json:"vrf_id,omitempty"`
	MinVID      *int    `json:"min_vid,omitempty"`
	MaxVID      *int    `json:"max_vid,omitempty"`
}

// ListVLANs returns GET /vlans.
func (c *Client) ListVLANs(ctx context.Context, p ListParams) (*Page[models.VLAN], error) {
	var page Page[models.VLAN]
	if err := c.get(ctx, "/api/v1/vlans", p.Values(), &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetVLAN returns GET /vlans/{vlan_id}.
func (c *Client) GetVLAN(ctx context.Context, id string) (*models.VLAN, error) {
	var v models.VLAN
	if err := c.get(ctx, "/api/v1/vlans/"+id, nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// ListVLANPrefixes returns GET /vlans/{vlan_id}/prefixes.
func (c *Client) ListVLANPrefixes(ctx context.Context, id string) (*Page[models.IPAMPrefix], error) {
	var page Page[models.IPAMPrefix]
	if err := c.get(ctx, "/api/v1/vlans/"+id+"/prefixes", nil, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// CreateVLAN returns POST /vlans.
func (c *Client) CreateVLAN(ctx context.Context, req VLANCreateRequest) (*models.VLAN, error) {
	var v models.VLAN
	if err := c.post(ctx, "/api/v1/vlans", req, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// UpdateVLAN returns PUT /vlans/{vlan_id}.
func (c *Client) UpdateVLAN(ctx context.Context, id string, req VLANUpdateRequest) (*models.VLAN, error) {
	var v models.VLAN
	if err := c.put(ctx, "/api/v1/vlans/"+id, req, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// DeleteVLAN returns DELETE /vlans/{vlan_id}.
func (c *Client) DeleteVLAN(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/vlans/%s", id))
}

// ListVLANGroups returns GET /vlan-groups.
func (c *Client) ListVLANGroups(ctx context.Context, p ListParams) (*Page[models.VLANGroup], error) {
	var page Page[models.VLANGroup]
	if err := c.get(ctx, "/api/v1/vlan-groups", p.Values(), &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// CreateVLANGroup returns POST /vlan-groups.
func (c *Client) CreateVLANGroup(ctx context.Context, req VLANGroupRequest) (*models.VLANGroup, error) {
	var g models.VLANGroup
	if err := c.post(ctx, "/api/v1/vlan-groups", req, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// DeleteVLANGroup returns DELETE /vlan-groups/{group_id}.
func (c *Client) DeleteVLANGroup(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/vlan-groups/%s", id))
}
