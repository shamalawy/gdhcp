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

// LeaseBulkRequest is the body of POST /leases/bulk: one action
// ("release", "abandon", "renew" or "delete") applied to a selection of
// leases.
type LeaseBulkRequest struct {
	Action          string   `json:"action"`
	LeaseIDs        []string `json:"lease_ids"`
	DurationSeconds int64    `json:"duration_seconds,omitempty"`
}

// LeaseBulkResult is one lease's outcome inside a bulk response.
type LeaseBulkResult struct {
	LeaseID string `json:"lease_id"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// LeaseBulkResponse is the body of a successful POST /leases/bulk. A bulk
// action reports per-lease outcomes rather than failing as a whole, so
// Failed > 0 alongside Succeeded > 0 is a normal result, not a contradiction.
type LeaseBulkResponse struct {
	Action    string            `json:"action"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
	Results   []LeaseBulkResult `json:"results"`
}

// ReleaseLease posts POST /leases/{lease_id}/release.
func (c *Client) ReleaseLease(ctx context.Context, id string) (string, error) {
	var out messageResponse
	if err := c.post(ctx, "/api/v1/leases/"+id+"/release", nil, &out); err != nil {
		return "", err
	}
	return out.Message, nil
}

// AbandonLease posts POST /leases/{lease_id}/abandon.
func (c *Client) AbandonLease(ctx context.Context, id string) (string, error) {
	var out messageResponse
	if err := c.post(ctx, "/api/v1/leases/"+id+"/abandon", nil, &out); err != nil {
		return "", err
	}
	return out.Message, nil
}

// RenewLease posts POST /leases/{lease_id}/renew. durationSeconds of 0
// reuses the lease's own original duration.
func (c *Client) RenewLease(ctx context.Context, id string, durationSeconds int64) (*models.DHCPLease, error) {
	var l models.DHCPLease
	body := map[string]any{"duration_seconds": durationSeconds}
	if err := c.post(ctx, "/api/v1/leases/"+id+"/renew", body, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// DeleteLease sends DELETE /leases/{lease_id}.
func (c *Client) DeleteLease(ctx context.Context, id string) error {
	return c.delete(ctx, "/api/v1/leases/"+id)
}

// BulkLeaseAction posts POST /leases/bulk.
func (c *Client) BulkLeaseAction(ctx context.Context, req LeaseBulkRequest) (*LeaseBulkResponse, error) {
	var out LeaseBulkResponse
	if err := c.post(ctx, "/api/v1/leases/bulk", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
