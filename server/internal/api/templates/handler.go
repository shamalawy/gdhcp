// Package templates implements the Template Endpoints of
// docs/api/endpoints.md.
package templates

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/api/options"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// List handles GET /api/v1/templates.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := st.ListTemplates(r.Context(), httpx.ParseListParams(r))
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// Get handles GET /api/v1/templates/{template_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, err := st.GetTemplateByID(r.Context(), mux.Vars(r)["template_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, t)
	}
}

type upsertBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	IsEnabled   *bool   `json:"is_enabled"`
}

// Create handles POST /api/v1/templates.
func Create(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body upsertBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("name", body.Name); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		t := &models.Template{Name: body.Name, Description: body.Description, IsEnabled: true}
		if err := st.CreateTemplate(r.Context(), t); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, t)
	}
}

// Update handles PUT /api/v1/templates/{template_id}.
func Update(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["template_id"]
		existing, err := st.GetTemplateByID(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		var body upsertBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("name", body.Name); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		existing.Name = body.Name
		existing.Description = body.Description
		if body.IsEnabled != nil {
			existing.IsEnabled = *body.IsEnabled
		}
		if err := st.UpdateTemplate(r.Context(), existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// Delete handles DELETE /api/v1/templates/{template_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteTemplate(r.Context(), mux.Vars(r)["template_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "template deleted")
	}
}

// GetOptions handles GET /api/v1/templates/{template_id}/options.
func GetOptions(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts, err := st.GetOptionsByTemplate(r.Context(), mux.Vars(r)["template_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, options.AttachCatalog(r.Context(), st, opts))
	}
}

type setOptionBody struct {
	OptionCode int16  `json:"option_code"`
	Value      string `json:"value"`
}

// SetOption handles POST /api/v1/templates/{template_id}/options.
func SetOption(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		templateID := mux.Vars(r)["template_id"]
		var body setOptionBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if body.OptionCode <= 0 {
			httpx.RespondError(w, r, log, httpx.Invalid("option_code is required", "field", "option_code"))
			return
		}
		ov := &models.DHCPOption{
			OptionCode:    body.OptionCode,
			ScopeLevel:    models.OptionScopeTemplate,
			ScopeEntityID: templateID,
			Value:         body.Value,
			IsEnabled:     true,
		}
		if err := st.CreateOptionValue(r.Context(), ov); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		ov.Catalog, _ = st.GetOptionCatalogEntry(r.Context(), ov.OptionCode)
		httpx.RespondCreated(w, ov)
	}
}

// ListScopeAssocs handles GET /api/v1/templates/{template_id}/scopes.
func ListScopeAssocs(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assocs, err := st.ListTemplateScopeAssocs(r.Context(), mux.Vars(r)["template_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, assocs)
	}
}

type assocBody struct {
	Priority int `json:"priority"`
}

// AssociateScope handles POST /api/v1/templates/{template_id}/scopes/{scope_id}.
func AssociateScope(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		var body assocBody
		_ = httpx.DecodeJSON(r, &body) // priority is optional; malformed/empty body -> default 0
		assoc, err := st.AssociateTemplateScope(r.Context(), vars["template_id"], vars["scope_id"], body.Priority)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, assoc)
	}
}

// DisassociateScope handles DELETE /api/v1/templates/{template_id}/scopes/{scope_id}.
func DisassociateScope(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if err := st.DisassociateTemplateScope(r.Context(), vars["template_id"], vars["scope_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "template disassociated from scope")
	}
}
