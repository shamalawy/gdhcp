// Package tags implements the Tag Endpoints of docs/api/endpoints.md.
package tags

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// List handles GET /api/v1/tags?entity_type&entity_id.
//
// With both entity filters set it returns just that entity's tags (via
// ListTagsForEntity); otherwise it returns the global tag catalogue. Without
// the filters every detail page rendered the whole catalogue as if it were
// attached to the entity being viewed, which made tags meaningless.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := httpx.ParseListParams(r, "entity_type", "entity_id")
		entityType := models.TaggableEntityType(params.Filters["entity_type"])
		entityID := params.Filters["entity_id"]

		if entityType != "" || entityID != "" {
			if entityType == "" || entityID == "" {
				httpx.RespondError(w, r, log,
					httpx.Invalid("entity_type and entity_id must be supplied together", "field", "entity_id"))
				return
			}
			if err := validEntityType(entityType); err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
			items, err := st.ListTagsForEntity(r.Context(), entityType, entityID)
			if err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
			httpx.RespondList(w, items, int64(len(items)), len(items), 0)
			return
		}

		res, err := st.ListTags(r.Context(), params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

type createBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Color       *string `json:"color"`
}

// Create handles POST /api/v1/tags.
func Create(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("name", body.Name); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		t := &models.Tag{Name: body.Name, Description: body.Description, Color: body.Color}
		if err := st.CreateTag(r.Context(), t); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, t)
	}
}

// Delete handles DELETE /api/v1/tags/{tag_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteTag(r.Context(), mux.Vars(r)["tag_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "tag deleted")
	}
}

type addEntityBody struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
}

// AddEntity handles POST /api/v1/tags/{tag_id}/entities.
func AddEntity(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tagID := mux.Vars(r)["tag_id"]
		var body addEntityBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		entityType := models.TaggableEntityType(body.EntityType)
		if err := validEntityType(entityType); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("entity_id", body.EntityID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		et, err := st.AddEntityTag(r.Context(), tagID, entityType, body.EntityID)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, et)
	}
}

// RemoveEntity handles DELETE /api/v1/tags/{tag_id}/entities/{entity_type}/{entity_id}.
func RemoveEntity(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		entityType := models.TaggableEntityType(vars["entity_type"])
		if err := validEntityType(entityType); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := st.RemoveEntityTag(r.Context(), vars["tag_id"], entityType, vars["entity_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "tag removed from entity")
	}
}

func validEntityType(t models.TaggableEntityType) error {
	switch t {
	case models.TaggableVRF, models.TaggablePrefix, models.TaggableVLAN,
		models.TaggableScope, models.TaggablePool,
		models.TaggableReservation, models.TaggableServer, models.TaggableCluster:
		return nil
	default:
		return httpx.Invalid("entity_type must be one of vrf, prefix, vlan, scope, pool, reservation, server, cluster", "field", "entity_type")
	}
}
