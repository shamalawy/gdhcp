package options

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// AttachCatalog populates each option's Catalog field by looking up its
// OptionCode in the catalog store, so every option-value response carries
// the option's human-readable name/value_type/description (quality bar
// requirement) without the frontend making a second round trip. Exported for
// reuse by the templates package (whose /templates/{id}/options endpoints
// return the same DHCPOption/OptionValue shape).
func AttachCatalog(ctx context.Context, catalog store.OptionCatalogStore, opts []*models.DHCPOption) []*models.DHCPOption {
	cache := make(map[int16]*models.OptionCatalogEntry)
	for _, o := range opts {
		entry, ok := cache[o.OptionCode]
		if !ok {
			entry, _ = catalog.GetOptionCatalogEntry(ctx, o.OptionCode) // best-effort: leave Catalog nil if lookup fails
			cache[o.OptionCode] = entry
		}
		o.Catalog = entry
	}
	return opts
}

type setValueBody struct {
	OptionCode int16  `json:"option_code"`
	Value      string `json:"value"`
}

// GetScopeOptions handles GET /api/v1/scopes/{scope_id}/options.
func GetScopeOptions(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts, err := st.GetOptionsByScope(r.Context(), mux.Vars(r)["scope_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, AttachCatalog(r.Context(), st, opts))
	}
}

// SetScopeOption handles POST /api/v1/scopes/{scope_id}/options.
func SetScopeOption(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		createValue(w, r, st, log, models.OptionScopeScope, mux.Vars(r)["scope_id"])
	}
}

// UpdateScopeOption handles PUT /api/v1/scopes/{scope_id}/options/{option_id}.
func UpdateScopeOption(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { updateValue(w, r, st, log) }
}

// DeleteScopeOption handles DELETE /api/v1/scopes/{scope_id}/options/{option_id}.
func DeleteScopeOption(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { deleteValue(w, r, st, log) }
}

// GetPoolOptions handles GET /api/v1/pools/{pool_id}/options.
func GetPoolOptions(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts, err := st.GetOptionsByPool(r.Context(), mux.Vars(r)["pool_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, AttachCatalog(r.Context(), st, opts))
	}
}

// SetPoolOption handles POST /api/v1/pools/{pool_id}/options.
func SetPoolOption(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		createValue(w, r, st, log, models.OptionScopePool, mux.Vars(r)["pool_id"])
	}
}

// DeletePoolOption handles DELETE /api/v1/pools/{pool_id}/options/{option_id}.
func DeletePoolOption(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { deleteValue(w, r, st, log) }
}

func createValue(w http.ResponseWriter, r *http.Request, st store.Store, log *slog.Logger, level models.OptionScopeLevel, entityID string) {
	var body setValueBody
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
		ScopeLevel:    level,
		ScopeEntityID: entityID,
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

type updateValueBody struct {
	Value     string `json:"value"`
	IsEnabled *bool  `json:"is_enabled"`
}

func updateValue(w http.ResponseWriter, r *http.Request, st store.Store, log *slog.Logger) {
	id := mux.Vars(r)["option_id"]
	var body updateValueBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.RespondError(w, r, log, err)
		return
	}
	isEnabled := true
	if body.IsEnabled != nil {
		isEnabled = *body.IsEnabled
	}
	ov, err := st.UpdateOptionValue(r.Context(), id, body.Value, isEnabled)
	if err != nil {
		httpx.RespondError(w, r, log, err)
		return
	}
	ov.Catalog, _ = st.GetOptionCatalogEntry(r.Context(), ov.OptionCode)
	httpx.RespondOK(w, ov)
}

func deleteValue(w http.ResponseWriter, r *http.Request, st store.Store, log *slog.Logger) {
	if err := st.DeleteOptionValue(r.Context(), mux.Vars(r)["option_id"]); err != nil {
		httpx.RespondError(w, r, log, err)
		return
	}
	httpx.RespondMessage(w, http.StatusOK, "option value removed")
}
