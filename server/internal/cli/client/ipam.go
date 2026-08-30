package client

import (
	"context"
	"fmt"
	"net/url"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// PrefixCreateRequest is the request body for POST /prefixes.
type PrefixCreateRequest struct {
	VRFID       string  `json:"vrf_id"`
	CIDR        string  `json:"cidr"`
	Type        string  `json:"type"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// PrefixUpdateRequest is the request body for PUT /prefixes/{prefix_id}.
type PrefixUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Type        *string `json:"type,omitempty"`
}

// ListPrefixes returns GET /prefixes, optionally filtered by vrfID/prefixType
// (either may be empty).
func (c *Client) ListPrefixes(ctx context.Context, p ListParams, vrfID, prefixType string) (*Page[models.IPAMPrefix], error) {
	q := p.Values()
	if vrfID != "" {
		q.Set("vrf_id", vrfID)
	}
	if prefixType != "" {
		q.Set("type", prefixType)
	}
	var page Page[models.IPAMPrefix]
	if err := c.get(ctx, "/api/v1/prefixes", q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetPrefix returns GET /prefixes/{prefix_id}.
func (c *Client) GetPrefix(ctx context.Context, id string) (*models.IPAMPrefix, error) {
	var p models.IPAMPrefix
	if err := c.get(ctx, "/api/v1/prefixes/"+id, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreatePrefix returns POST /prefixes.
func (c *Client) CreatePrefix(ctx context.Context, req PrefixCreateRequest) (*models.IPAMPrefix, error) {
	var p models.IPAMPrefix
	if err := c.post(ctx, "/api/v1/prefixes", req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdatePrefix returns PUT /prefixes/{prefix_id}.
func (c *Client) UpdatePrefix(ctx context.Context, id string, req PrefixUpdateRequest) (*models.IPAMPrefix, error) {
	var p models.IPAMPrefix
	if err := c.put(ctx, "/api/v1/prefixes/"+id, req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// DeletePrefix returns DELETE /prefixes/{prefix_id}.
func (c *Client) DeletePrefix(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/prefixes/%s", id))
}

// IPAMTree returns GET /ipam/tree. Any of vrfID/parentID may be empty; depth
// <= 0 omits the depth parameter (server default applies).
func (c *Client) IPAMTree(ctx context.Context, vrfID, parentID string, depth int) ([]*models.IPAMTreeNode, error) {
	q := url.Values{}
	if vrfID != "" {
		q.Set("vrf_id", vrfID)
	}
	if parentID != "" {
		q.Set("parent_id", parentID)
	}
	if depth > 0 {
		q.Set("depth", fmt.Sprintf("%d", depth))
	}
	var nodes []*models.IPAMTreeNode
	if err := c.get(ctx, "/api/v1/ipam/tree", q, &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

// ListPrefixClients returns GET /prefixes/{prefix_id}/clients — DHCP clients
// (leases and enabled reservations) rolled up from the prefix and all
// descendant prefixes, with optional filters by kind, state, and search.
func (c *Client) ListPrefixClients(ctx context.Context, prefixID string, p ListParams, kind, state string) (*Page[models.DHCPClient], error) {
	q := p.Values()
	if kind != "" {
		q.Set("kind", kind)
	}
	if state != "" {
		q.Set("state", state)
	}
	var page Page[models.DHCPClient]
	if err := c.get(ctx, fmt.Sprintf("/api/v1/prefixes/%s/clients", prefixID), q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// Search returns GET /search. entityTypes may be empty (search all types);
// limit <= 0 omits the limit parameter.
func (c *Client) Search(ctx context.Context, query string, entityTypes []string, limit int) ([]*models.SearchResult, error) {
	q := url.Values{}
	q.Set("q", query)
	for _, t := range entityTypes {
		q.Add("entity_types", t)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	var results []*models.SearchResult
	if err := c.get(ctx, "/api/v1/search", q, &results); err != nil {
		return nil, err
	}
	return results, nil
}
