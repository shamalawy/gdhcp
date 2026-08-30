package client

import (
	"context"
	"fmt"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// TagCreateRequest is the request body for POST /tags.
type TagCreateRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Color       *string `json:"color,omitempty"`
}

// ListTags returns GET /tags.
func (c *Client) ListTags(ctx context.Context, p ListParams) ([]*models.Tag, error) {
	var tags []*models.Tag
	if err := c.get(ctx, "/api/v1/tags", p.Values(), &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

// CreateTag returns POST /tags.
func (c *Client) CreateTag(ctx context.Context, req TagCreateRequest) (*models.Tag, error) {
	var t models.Tag
	if err := c.post(ctx, "/api/v1/tags", req, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTag returns DELETE /tags/{tag_id}.
func (c *Client) DeleteTag(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/tags/%s", id))
}

// TagEntity returns POST /tags/{tag_id}/entities: attaches tagID to the
// entity identified by entityType/entityID.
func (c *Client) TagEntity(ctx context.Context, tagID, entityType, entityID string) (*models.EntityTag, error) {
	req := struct {
		EntityType string `json:"entity_type"`
		EntityID   string `json:"entity_id"`
	}{EntityType: entityType, EntityID: entityID}
	var et models.EntityTag
	if err := c.post(ctx, fmt.Sprintf("/api/v1/tags/%s/entities", tagID), req, &et); err != nil {
		return nil, err
	}
	return &et, nil
}

// UntagEntity returns DELETE /tags/{tag_id}/entities/{entity_type}/{entity_id}.
func (c *Client) UntagEntity(ctx context.Context, tagID, entityType, entityID string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/tags/%s/entities/%s/%s", tagID, entityType, entityID))
}
