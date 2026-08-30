// Package vlans implements the VLAN and VLAN-group endpoints. VLANs record
// layer-2 segmentation and are assigned to IPAM prefixes via
// ipam_prefixes.vlan_id (migration 0004).
package vlans

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// ---------------------------------------------------------------------------
// VLANs
// ---------------------------------------------------------------------------

// List handles GET /api/v1/vlans?group_id&status. group_id=none selects
// VLANs that belong to no group.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := httpx.ParseListParams(r, "group_id", "status")
		res, err := st.ListVLANs(r.Context(), params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// Get handles GET /api/v1/vlans/{vlan_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := st.GetVLANByID(r.Context(), mux.Vars(r)["vlan_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, v)
	}
}

// Prefixes handles GET /api/v1/vlans/{vlan_id}/prefixes: the IPAM prefixes
// assigned to this VLAN.
func Prefixes(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["vlan_id"]
		if _, err := st.GetVLANByID(r.Context(), id); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		items, err := st.ListPrefixesByVLAN(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, items, int64(len(items)), len(items), 0)
	}
}

type vlanBody struct {
	GroupID     *string `json:"group_id"`
	VID         *int    `json:"vid"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
}

// validate applies the same rules migration 0004's CHECK constraints do, so
// the client gets a 400 with a useful message instead of a 409 from the DB.
func (b vlanBody) validate(requireVID bool) error {
	if err := httpx.RequireString("name", b.Name); err != nil {
		return err
	}
	if b.VID == nil {
		if requireVID {
			return httpx.Invalid("vid is required", "field", "vid")
		}
	} else if *b.VID < models.MinVID || *b.VID > models.MaxVID {
		return httpx.Invalid("vid must be between 1 and 4094 (0 and 4095 are reserved by 802.1Q)", "field", "vid")
	}
	if b.Status != "" && !models.VLANStatus(b.Status).Valid() {
		return httpx.Invalid("status must be one of active, reserved, deprecated", "field", "status")
	}
	return nil
}

// Create handles POST /api/v1/vlans.
func Create(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body vlanBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := body.validate(true); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := checkGroup(r, st, body.GroupID, *body.VID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		v := &models.VLAN{
			GroupID:     body.GroupID,
			VID:         *body.VID,
			Name:        body.Name,
			Description: body.Description,
			Status:      models.VLANStatus(body.Status),
		}
		if err := st.CreateVLAN(r.Context(), v); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, v)
	}
}

// Update handles PUT /api/v1/vlans/{vlan_id}.
func Update(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, err := st.GetVLANByID(r.Context(), mux.Vars(r)["vlan_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body vlanBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := body.validate(false); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		existing.Name = body.Name
		existing.Description = body.Description
		existing.GroupID = body.GroupID
		if body.VID != nil {
			existing.VID = *body.VID
		}
		if body.Status != "" {
			existing.Status = models.VLANStatus(body.Status)
		}
		if err := checkGroup(r, st, existing.GroupID, existing.VID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		if err := st.UpdateVLAN(r.Context(), existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// checkGroup verifies the target group exists and that the VID falls inside
// the range that group reserves.
func checkGroup(r *http.Request, st store.Store, groupID *string, vid int) error {
	if groupID == nil || *groupID == "" {
		return nil
	}
	g, err := st.GetVLANGroupByID(r.Context(), *groupID)
	if err != nil {
		return err
	}
	if vid < g.MinVID || vid > g.MaxVID {
		return httpx.Invalid("vid is outside the group's allowed range", "field", "vid")
	}
	return nil
}

// Delete handles DELETE /api/v1/vlans/{vlan_id}. Prefixes assigned to the
// VLAN are unassigned, not deleted.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteVLAN(r.Context(), mux.Vars(r)["vlan_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "vlan deleted")
	}
}

// ---------------------------------------------------------------------------
// VLAN groups
// ---------------------------------------------------------------------------

// ListGroups handles GET /api/v1/vlan-groups.
func ListGroups(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := st.ListVLANGroups(r.Context(), httpx.ParseListParams(r))
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// GetGroup handles GET /api/v1/vlan-groups/{group_id}.
func GetGroup(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g, err := st.GetVLANGroupByID(r.Context(), mux.Vars(r)["group_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, g)
	}
}

type groupBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	VRFID       *string `json:"vrf_id"`
	MinVID      *int    `json:"min_vid"`
	MaxVID      *int    `json:"max_vid"`
}

// applyTo validates the body and copies it onto g, defaulting the range to
// the full 802.1Q span when unspecified.
func (b groupBody) applyTo(g *models.VLANGroup) error {
	if err := httpx.RequireString("name", b.Name); err != nil {
		return err
	}
	minVID, maxVID := models.MinVID, models.MaxVID
	if b.MinVID != nil {
		minVID = *b.MinVID
	}
	if b.MaxVID != nil {
		maxVID = *b.MaxVID
	}
	if minVID < models.MinVID || maxVID > models.MaxVID || minVID > maxVID {
		return httpx.Invalid("min_vid/max_vid must satisfy 1 <= min_vid <= max_vid <= 4094", "field", "min_vid")
	}
	g.Name, g.Description, g.VRFID = b.Name, b.Description, b.VRFID
	g.MinVID, g.MaxVID = minVID, maxVID
	return nil
}

// CreateGroup handles POST /api/v1/vlan-groups.
func CreateGroup(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body groupBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		g := &models.VLANGroup{}
		if err := body.applyTo(g); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := st.CreateVLANGroup(r.Context(), g); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, g)
	}
}

// UpdateGroup handles PUT /api/v1/vlan-groups/{group_id}.
func UpdateGroup(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, err := st.GetVLANGroupByID(r.Context(), mux.Vars(r)["group_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		var body groupBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := body.applyTo(existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := st.UpdateVLANGroup(r.Context(), existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// DeleteGroup handles DELETE /api/v1/vlan-groups/{group_id}. Member VLANs
// survive as ungrouped.
func DeleteGroup(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteVLANGroup(r.Context(), mux.Vars(r)["group_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "vlan group deleted")
	}
}
