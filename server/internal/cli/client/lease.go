package client

import (
	"context"
	"net/url"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// ListLeases returns GET /leases: all leases, optionally filtered by
// scopeID/poolID/state (any may be empty).
func (c *Client) ListLeases(ctx context.Context, p ListParams, scopeID, poolID, state string) (*Page[models.DHCPLease], error) {
	q := p.Values()
	if scopeID != "" {
		q.Set("scope_id", scopeID)
	}
	if poolID != "" {
		q.Set("pool_id", poolID)
	}
	if state != "" {
		q.Set("state", state)
	}
	var page Page[models.DHCPLease]
	if err := c.get(ctx, "/api/v1/leases", q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetLease returns GET /leases/{lease_id}.
func (c *Client) GetLease(ctx context.Context, id string) (*models.DHCPLease, error) {
	var l models.DHCPLease
	if err := c.get(ctx, "/api/v1/leases/"+id, nil, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// GetLeaseByIP returns GET /leases/by-ip/{ip_address}.
func (c *Client) GetLeaseByIP(ctx context.Context, ip string) (*models.DHCPLease, error) {
	var l models.DHCPLease
	if err := c.get(ctx, "/api/v1/leases/by-ip/"+url.PathEscape(ip), nil, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// GetLeasesByMAC returns GET /leases/by-mac/{mac_address}.
func (c *Client) GetLeasesByMAC(ctx context.Context, mac string) ([]*models.DHCPLease, error) {
	var leases []*models.DHCPLease
	if err := c.get(ctx, "/api/v1/leases/by-mac/"+url.PathEscape(mac), nil, &leases); err != nil {
		return nil, err
	}
	return leases, nil
}
