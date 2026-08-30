package client

import (
	"context"
	"fmt"
	"net/url"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// ScopeCreateRequest is the request body for POST /scopes.
type ScopeCreateRequest struct {
	VRFID        string  `json:"vrf_id"`
	ClusterID    string  `json:"cluster_id"`
	IPAMPrefixID string  `json:"ipam_prefix_id"`
	Name         string  `json:"name"`
	Description  *string `json:"description,omitempty"`
}

// ScopeUpdateRequest is the request body for PUT /scopes/{scope_id}.
type ScopeUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	IsEnabled   *bool   `json:"is_enabled,omitempty"`
}

// ListScopes returns GET /scopes, optionally filtered by vrfID/clusterID.
func (c *Client) ListScopes(ctx context.Context, p ListParams, vrfID, clusterID string) (*Page[models.DHCPScope], error) {
	q := p.Values()
	if vrfID != "" {
		q.Set("vrf_id", vrfID)
	}
	if clusterID != "" {
		q.Set("cluster_id", clusterID)
	}
	var page Page[models.DHCPScope]
	if err := c.get(ctx, "/api/v1/scopes", q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetScope returns GET /scopes/{scope_id}.
func (c *Client) GetScope(ctx context.Context, id string) (*models.DHCPScope, error) {
	var s models.DHCPScope
	if err := c.get(ctx, "/api/v1/scopes/"+id, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateScope returns POST /scopes.
func (c *Client) CreateScope(ctx context.Context, req ScopeCreateRequest) (*models.DHCPScope, error) {
	var s models.DHCPScope
	if err := c.post(ctx, "/api/v1/scopes", req, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateScope returns PUT /scopes/{scope_id}.
func (c *Client) UpdateScope(ctx context.Context, id string, req ScopeUpdateRequest) (*models.DHCPScope, error) {
	var s models.DHCPScope
	if err := c.put(ctx, "/api/v1/scopes/"+id, req, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteScope returns DELETE /scopes/{scope_id}.
func (c *Client) DeleteScope(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/scopes/%s", id))
}

// ScopeUtilization returns GET /scopes/{scope_id}/utilization.
func (c *Client) ScopeUtilization(ctx context.Context, id string) (*models.Utilization, error) {
	var u models.Utilization
	if err := c.get(ctx, fmt.Sprintf("/api/v1/scopes/%s/utilization", id), nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// ScopeStats returns GET /scopes/{scope_id}/stats. window is passed through
// verbatim (e.g. "24h", "7d"); empty uses the server default.
func (c *Client) ScopeStats(ctx context.Context, id, window string) (*models.DHCPStats, error) {
	q := url.Values{}
	if window != "" {
		q.Set("window", window)
	}
	var s models.DHCPStats
	if err := c.get(ctx, fmt.Sprintf("/api/v1/scopes/%s/stats", id), q, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetScopeOptions returns GET /scopes/{scope_id}/options.
func (c *Client) GetScopeOptions(ctx context.Context, scopeID string) ([]*models.OptionValue, error) {
	var opts []*models.OptionValue
	if err := c.get(ctx, fmt.Sprintf("/api/v1/scopes/%s/options", scopeID), nil, &opts); err != nil {
		return nil, err
	}
	return opts, nil
}

// SetScopeOption returns POST /scopes/{scope_id}/options.
func (c *Client) SetScopeOption(ctx context.Context, scopeID string, optionCode int16, value string) (*models.OptionValue, error) {
	req := OptionValueSetRequest{OptionCode: optionCode, Value: value}
	var ov models.OptionValue
	if err := c.post(ctx, fmt.Sprintf("/api/v1/scopes/%s/options", scopeID), req, &ov); err != nil {
		return nil, err
	}
	return &ov, nil
}

// UpdateScopeOption returns PUT /scopes/{scope_id}/options/{option_id}.
func (c *Client) UpdateScopeOption(ctx context.Context, scopeID, optionID string, req OptionValueUpdateRequest) (*models.OptionValue, error) {
	var ov models.OptionValue
	if err := c.put(ctx, fmt.Sprintf("/api/v1/scopes/%s/options/%s", scopeID, optionID), req, &ov); err != nil {
		return nil, err
	}
	return &ov, nil
}

// UnsetScopeOption returns DELETE /scopes/{scope_id}/options/{option_id}.
func (c *Client) UnsetScopeOption(ctx context.Context, scopeID, optionID string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/scopes/%s/options/%s", scopeID, optionID))
}

// AttachTemplate returns POST /templates/{template_id}/scopes/{scope_id}.
func (c *Client) AttachTemplate(ctx context.Context, templateID, scopeID string, priority int) (*models.TemplateScopeAssoc, error) {
	req := struct {
		Priority int `json:"priority"`
	}{Priority: priority}
	var assoc models.TemplateScopeAssoc
	if err := c.post(ctx, fmt.Sprintf("/api/v1/templates/%s/scopes/%s", templateID, scopeID), req, &assoc); err != nil {
		return nil, err
	}
	return &assoc, nil
}

// DetachTemplate returns DELETE /templates/{template_id}/scopes/{scope_id}.
func (c *Client) DetachTemplate(ctx context.Context, templateID, scopeID string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/templates/%s/scopes/%s", templateID, scopeID))
}
