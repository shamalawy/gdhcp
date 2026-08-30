// Package ipam implements the IPAM Prefix Endpoints of docs/api/endpoints.md:
// prefix CRUD (this file), the lazy hierarchy tree (tree.go), and global
// search (search.go).
package ipam

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// List handles GET /api/v1/prefixes?vrf_id&type&parent_id. parent_id is
// optional: the literal "root" filters to prefixes with no parent, any other
// value filters to exact children of that prefix, and omitting it returns
// the flat unscoped list (see store.Store.ListPrefixes).
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := httpx.ParseListParams(r, "vrf_id", "type", "parent_id")
		res, err := st.ListPrefixes(r.Context(), params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// Get handles GET /api/v1/prefixes/{prefix_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := st.GetPrefixByID(r.Context(), mux.Vars(r)["prefix_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, p)
	}
}

type createBody struct {
	VRFID       string  `json:"vrf_id"`
	CIDR        string  `json:"cidr"`
	Type        string  `json:"type"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	VLANID      *string `json:"vlan_id"`
}

// Create handles POST /api/v1/prefixes. The parent prefix is not supplied by
// the client — it is derived by finding the narrowest existing prefix in the
// VRF that contains the new CIDR (nil parent means a new top-level
// aggregate), matching the ipam_prefixes hierarchy model.
func Create(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("vrf_id", body.VRFID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		ipnet, err := httpx.ParseCIDR("cidr", body.CIDR)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		prefixType := models.PrefixType(body.Type)
		if !prefixType.Valid() {
			httpx.RespondError(w, r, log, httpx.Invalid("type must be one of aggregate, container, subnet", "field", "type"))
			return
		}

		parent, err := st.FindPrefixContaining(r.Context(), body.VRFID, ipnet)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		mn := models.IPNet(*ipnet)
		p := &models.IPAMPrefix{
			VRFID:       body.VRFID,
			Prefix:      &mn,
			PrefixType:  prefixType,
			Name:        body.Name,
			Description: body.Description,
			VLANID:      nilIfBlank(body.VLANID),
		}
		if parent != nil {
			p.ParentID = &parent.ID
		}

		if err := st.CreatePrefix(r.Context(), p); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, p)
	}
}

type updateBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Type        string  `json:"type"`
	VLANID      *string `json:"vlan_id"`
}

// nilIfBlank normalizes an omitted ("" or absent) optional UUID to a real
// NULL, so clearing a VLAN assignment doesn't try to store an empty string
// in a uuid column.
func nilIfBlank(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

// Update handles PUT /api/v1/prefixes/{prefix_id}.
func Update(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["prefix_id"]
		existing, err := st.GetPrefixByID(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body updateBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if body.Type != "" {
			prefixType := models.PrefixType(body.Type)
			if !prefixType.Valid() {
				httpx.RespondError(w, r, log, httpx.Invalid("type must be one of aggregate, container, subnet", "field", "type"))
				return
			}
			existing.PrefixType = prefixType
		}
		existing.Name = body.Name
		existing.Description = body.Description
		existing.VLANID = nilIfBlank(body.VLANID)

		if err := st.UpdatePrefix(r.Context(), existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// Delete handles DELETE /api/v1/prefixes/{prefix_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeletePrefix(r.Context(), mux.Vars(r)["prefix_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "prefix deleted")
	}
}

// Ancestors handles GET /api/v1/prefixes/{prefix_id}/ancestors: the chain
// from the VRF's top-level prefix down to this prefix's immediate parent,
// root first. Drives the detail page's breadcrumb trail and lets the tree
// expand straight to a deep-linked node.
func Ancestors(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["prefix_id"]
		if _, err := st.GetPrefixByID(r.Context(), id); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		items, err := st.GetAncestorPrefixes(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, items, int64(len(items)), len(items), 0)
	}
}
