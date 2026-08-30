package client

import (
	"context"
	"fmt"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// OptionValueSetRequest is the request body for POST .../options
// (scope/pool/template option-value creation, docs/api/endpoints.md).
type OptionValueSetRequest struct {
	OptionCode int16  `json:"option_code"`
	Value      string `json:"value"`
}

// OptionValueUpdateRequest is the request body for
// PUT /scopes/{scope_id}/options/{option_id}.
type OptionValueUpdateRequest struct {
	Value     *string `json:"value,omitempty"`
	IsEnabled *bool   `json:"is_enabled,omitempty"`
}

// OptionCreateRequest is the request body for POST /options (custom
// option definitions, as opposed to option *values*).
type OptionCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Code        int16  `json:"code"`
	ValueType   string `json:"value_type"`
}

// ListOptions returns GET /options.
func (c *Client) ListOptions(ctx context.Context, p ListParams, standard, custom bool) ([]*models.DHCPOption, error) {
	q := p.Values()
	if standard {
		q.Set("standard", "true")
	}
	if custom {
		q.Set("custom", "true")
	}
	var opts []*models.DHCPOption
	if err := c.get(ctx, "/api/v1/options", q, &opts); err != nil {
		return nil, err
	}
	return opts, nil
}

// OptionCatalog returns GET /options/catalog: the full IANA DHCPv4 option
// catalog (amendment A5.4), unpaginated.
func (c *Client) OptionCatalog(ctx context.Context) ([]*models.OptionCatalogEntry, error) {
	var entries []*models.OptionCatalogEntry
	if err := c.get(ctx, "/api/v1/options/catalog", nil, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// GetOption returns GET /options/{option_id}.
func (c *Client) GetOption(ctx context.Context, id string) (*models.DHCPOption, error) {
	var o models.DHCPOption
	if err := c.get(ctx, "/api/v1/options/"+id, nil, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// CreateOption returns POST /options (custom option definition).
func (c *Client) CreateOption(ctx context.Context, req OptionCreateRequest) (*models.DHCPOption, error) {
	var o models.DHCPOption
	if err := c.post(ctx, "/api/v1/options", req, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// DeleteOption returns DELETE /options/{option_id}.
func (c *Client) DeleteOption(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/options/%s", id))
}

// GetPoolOptions returns GET /pools/{pool_id}/options.
func (c *Client) GetPoolOptions(ctx context.Context, poolID string) ([]*models.OptionValue, error) {
	var opts []*models.OptionValue
	if err := c.get(ctx, fmt.Sprintf("/api/v1/pools/%s/options", poolID), nil, &opts); err != nil {
		return nil, err
	}
	return opts, nil
}

// SetPoolOption returns POST /pools/{pool_id}/options.
func (c *Client) SetPoolOption(ctx context.Context, poolID string, optionCode int16, value string) (*models.OptionValue, error) {
	req := OptionValueSetRequest{OptionCode: optionCode, Value: value}
	var ov models.OptionValue
	if err := c.post(ctx, fmt.Sprintf("/api/v1/pools/%s/options", poolID), req, &ov); err != nil {
		return nil, err
	}
	return &ov, nil
}

// UnsetPoolOption returns DELETE /pools/{pool_id}/options/{option_id}.
func (c *Client) UnsetPoolOption(ctx context.Context, poolID, optionID string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/pools/%s/options/%s", poolID, optionID))
}
