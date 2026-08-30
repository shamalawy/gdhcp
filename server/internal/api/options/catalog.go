// Package options implements the DHCP Option Endpoints of
// docs/api/endpoints.md: the IANA/custom option catalog (this file) and
// option values at every scope level — global/template/scope/pool/
// reservation (values.go). Every option value response is joined against
// the catalog so the UI always has the option's name/value_type/description
// alongside its raw value (quality bar requirement).
//
// docs/api/endpoints.md names the catalog CRUD rows' response "DHCPOption"
// and gives POST /options a body of {name, description, code, value_type} —
// fields that belong to dhcp_option_catalog (models.OptionCatalogEntry), not
// the dhcp_option_values row CONTRACTS.md's Go type DHCPOption otherwise
// names. This file treats /options and /options/{option_id} as the catalog
// surface (keyed by the int16 option code) accordingly; see
// notes_for_integration for the full reasoning.
package options

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// ListCatalog handles GET /api/v1/options?standard&custom.
func ListCatalog(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := httpx.ParseListParams(r, "standard", "custom")
		res, err := st.ListOptionCatalog(r.Context(), params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// FullCatalog handles GET /api/v1/options/catalog: every IANA option,
// unpaginated.
func FullCatalog(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := st.GetAllOptionCatalog(r.Context())
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, entries)
	}
}

// GetCatalogEntry handles GET /api/v1/options/{option_id}.
func GetCatalogEntry(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code, err := parseOptionCode(mux.Vars(r)["option_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		entry, err := st.GetOptionCatalogEntry(r.Context(), code)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, entry)
	}
}

type createCatalogBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Code        int16  `json:"code"`
	ValueType   string `json:"value_type"`
}

// CreateCatalogEntry handles POST /api/v1/options: define a custom option
// code beyond the IANA-seeded catalog (migration 0002).
func CreateCatalogEntry(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createCatalogBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("name", body.Name); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if body.Code <= 0 || body.Code > 254 {
			httpx.RespondError(w, r, log, httpx.Invalid("code must be between 1 and 254", "field", "code"))
			return
		}
		valueType := models.OptionValueType(body.ValueType)
		if !valueType.Valid() {
			httpx.RespondError(w, r, log, httpx.Invalid("value_type is not a recognized option value type", "field", "value_type"))
			return
		}

		entry := &models.OptionCatalogEntry{
			OptionCode:  body.Code,
			Name:        body.Name,
			ValueType:   valueType,
			Description: body.Description,
		}
		if err := st.CreateOptionCatalogEntry(r.Context(), entry); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, entry)
	}
}

// DeleteCatalogEntry handles DELETE /api/v1/options/{option_id}.
func DeleteCatalogEntry(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code, err := parseOptionCode(mux.Vars(r)["option_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := st.DeleteOptionCatalogEntry(r.Context(), code); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "option catalog entry deleted")
	}
}

func parseOptionCode(s string) (int16, error) {
	n, err := strconv.ParseInt(s, 10, 16)
	if err != nil {
		return 0, httpx.Invalid("option_id must be a numeric option code", "field", "option_id")
	}
	return int16(n), nil
}
