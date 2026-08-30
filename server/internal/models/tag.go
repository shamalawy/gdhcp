package models

import "time"

// Tag represents a user-defined label (tags table) that can be attached to
// any taggable entity via EntityTag.
type Tag struct {
	ID              string    `json:"id" db:"id"`
	Name            string    `json:"name" db:"name"`
	Color           *string   `json:"color" db:"color"` // Hex color: #RRGGBB
	Description     *string   `json:"description" db:"description"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	CreatedByUserID *int64    `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
}

// TaggableEntityType enumerates the entity kinds that can carry tags
// (entity_tags.entity_type).
type TaggableEntityType string

const (
	TaggableVRF         TaggableEntityType = "vrf"
	TaggablePrefix      TaggableEntityType = "prefix"
	TaggableVLAN        TaggableEntityType = "vlan"
	TaggableScope       TaggableEntityType = "scope"
	TaggablePool        TaggableEntityType = "pool"
	TaggableReservation TaggableEntityType = "reservation"
	TaggableServer      TaggableEntityType = "server"
	TaggableCluster     TaggableEntityType = "cluster"
)

// EntityTag represents a many-to-many tag assignment (entity_tags table).
type EntityTag struct {
	ID         string             `json:"id" db:"id"`
	TagID      string             `json:"tag_id" db:"tag_id"`
	EntityType TaggableEntityType `json:"entity_type" db:"entity_type"`
	EntityID   string             `json:"entity_id" db:"entity_id"`
	CreatedAt  time.Time          `json:"created_at" db:"created_at"`

	// Tag is populated by list queries that join the tag row so callers get
	// the name/color without a second round trip.
	Tag *Tag `json:"tag,omitempty" db:"-"`
}
