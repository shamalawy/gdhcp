package client

import (
	"context"
	"fmt"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// TemplateCreateRequest is the request body for POST /templates.
type TemplateCreateRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// TemplateUpdateRequest is the request body for PUT /templates/{template_id}.
type TemplateUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	IsEnabled   *bool   `json:"is_enabled,omitempty"`
}

// ListTemplates returns GET /templates.
func (c *Client) ListTemplates(ctx context.Context, p ListParams) ([]*models.Template, error) {
	var templates []*models.Template
	if err := c.get(ctx, "/api/v1/templates", p.Values(), &templates); err != nil {
		return nil, err
	}
	return templates, nil
}

// GetTemplate returns GET /templates/{template_id}.
func (c *Client) GetTemplate(ctx context.Context, id string) (*models.Template, error) {
	var t models.Template
	if err := c.get(ctx, "/api/v1/templates/"+id, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateTemplate returns POST /templates.
func (c *Client) CreateTemplate(ctx context.Context, req TemplateCreateRequest) (*models.Template, error) {
	var t models.Template
	if err := c.post(ctx, "/api/v1/templates", req, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTemplate returns PUT /templates/{template_id}.
func (c *Client) UpdateTemplate(ctx context.Context, id string, req TemplateUpdateRequest) (*models.Template, error) {
	var t models.Template
	if err := c.put(ctx, "/api/v1/templates/"+id, req, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTemplate returns DELETE /templates/{template_id}.
func (c *Client) DeleteTemplate(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/templates/%s", id))
}

// GetTemplateOptions returns GET /templates/{template_id}/options.
func (c *Client) GetTemplateOptions(ctx context.Context, templateID string) ([]*models.OptionValue, error) {
	var opts []*models.OptionValue
	if err := c.get(ctx, fmt.Sprintf("/api/v1/templates/%s/options", templateID), nil, &opts); err != nil {
		return nil, err
	}
	return opts, nil
}

// SetTemplateOption returns POST /templates/{template_id}/options.
func (c *Client) SetTemplateOption(ctx context.Context, templateID string, optionCode int16, value string) (*models.OptionValue, error) {
	req := OptionValueSetRequest{OptionCode: optionCode, Value: value}
	var ov models.OptionValue
	if err := c.post(ctx, fmt.Sprintf("/api/v1/templates/%s/options", templateID), req, &ov); err != nil {
		return nil, err
	}
	return &ov, nil
}

// TemplateScopes returns GET /templates/{template_id}/scopes.
func (c *Client) TemplateScopes(ctx context.Context, templateID string) ([]*models.TemplateScopeAssoc, error) {
	var assocs []*models.TemplateScopeAssoc
	if err := c.get(ctx, fmt.Sprintf("/api/v1/templates/%s/scopes", templateID), nil, &assocs); err != nil {
		return nil, err
	}
	return assocs, nil
}
