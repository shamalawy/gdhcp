// Package clusters implements the Cluster & HA Endpoints of
// docs/api/endpoints.md, except POST /clusters/{id}/failover, which needs
// the api.HAStatus consumer-side interface (amendment A10) and so lives in
// package api itself (failover.go) to avoid this package depending on its
// parent.
package clusters

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// List handles GET /api/v1/clusters.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := st.ListClusters(r.Context(), httpx.ParseListParams(r))
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// Get handles GET /api/v1/clusters/{cluster_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := st.GetClusterByID(r.Context(), mux.Vars(r)["cluster_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, c)
	}
}

type upsertBody struct {
	Name                string  `json:"name"`
	Description         *string `json:"description"`
	VIPAddress          string  `json:"vip_address"`
	VIPInterface        *string `json:"vip_interface"`
	HeartbeatIntervalMs int     `json:"heartbeat_interval_ms"`
	HeartbeatTTLMs      int     `json:"heartbeat_ttl_ms"`
	ElectionIntervalMs  int     `json:"election_interval_ms"`
}

const (
	defaultHeartbeatIntervalMs = 1000
	defaultHeartbeatTTLMs      = 3000
	defaultElectionIntervalMs  = 2000
)

// Create handles POST /api/v1/clusters.
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

		c := &models.Cluster{
			Name:              body.Name,
			Description:       body.Description,
			VIPInterface:      body.VIPInterface,
			HeartbeatInterval: firstPositive(body.HeartbeatIntervalMs, defaultHeartbeatIntervalMs),
			HeartbeatTTL:      firstPositive(body.HeartbeatTTLMs, defaultHeartbeatTTLMs),
			ElectionInterval:  firstPositive(body.ElectionIntervalMs, defaultElectionIntervalMs),
		}
		if body.VIPAddress != "" {
			ip, err := httpx.ParseIP("vip_address", body.VIPAddress)
			if err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
			c.VIPAddress = &ip
		}

		if err := st.CreateCluster(r.Context(), c); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, c)
	}
}

// Update handles PUT /api/v1/clusters/{cluster_id}.
func Update(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["cluster_id"]
		existing, err := st.GetClusterByID(r.Context(), id)
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
		existing.VIPInterface = body.VIPInterface
		if body.VIPAddress != "" {
			ip, err := httpx.ParseIP("vip_address", body.VIPAddress)
			if err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
			existing.VIPAddress = &ip
		}
		existing.HeartbeatInterval = firstPositive(body.HeartbeatIntervalMs, existing.HeartbeatInterval)
		existing.HeartbeatTTL = firstPositive(body.HeartbeatTTLMs, existing.HeartbeatTTL)
		existing.ElectionInterval = firstPositive(body.ElectionIntervalMs, existing.ElectionInterval)

		if err := st.UpdateCluster(r.Context(), existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// Delete handles DELETE /api/v1/clusters/{cluster_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteCluster(r.Context(), mux.Vars(r)["cluster_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "cluster deleted")
	}
}

// ListServers handles GET /api/v1/clusters/{cluster_id}/servers.
func ListServers(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		servers, err := st.ListServers(r.Context(), mux.Vars(r)["cluster_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, servers)
	}
}

type registerServerBody struct {
	NodeName    string `json:"node_name"`
	NodeID      int    `json:"node_id"`
	APIAddress  string `json:"api_address"`
	DHCPAddress string `json:"dhcp_address"`
}

// RegisterServer handles POST /api/v1/clusters/{cluster_id}/servers.
func RegisterServer(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusterID := mux.Vars(r)["cluster_id"]
		if _, err := st.GetClusterByID(r.Context(), clusterID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body registerServerBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		for field, v := range map[string]string{
			"node_name": body.NodeName, "api_address": body.APIAddress, "dhcp_address": body.DHCPAddress,
		} {
			if err := httpx.RequireString(field, v); err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
		}

		srv := &models.ClusterServer{
			ClusterID:   clusterID,
			NodeName:    body.NodeName,
			NodeID:      body.NodeID,
			APIAddress:  body.APIAddress,
			DHCPAddress: body.DHCPAddress,
		}
		if err := st.RegisterServer(r.Context(), srv); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, srv)
	}
}

// DeregisterServer handles DELETE /api/v1/clusters/{cluster_id}/servers/{server_id}.
func DeregisterServer(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if err := st.DeregisterServer(r.Context(), vars["cluster_id"], vars["server_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "server deregistered")
	}
}

// Health handles GET /api/v1/clusters/{cluster_id}/health: an on-demand
// snapshot computed from stored heartbeats. The SSE `cluster` event
// (amendment A12) instead pushes HAStatus's live in-process view — see
// package api's events.go.
func Health(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, err := st.GetClusterHealth(r.Context(), mux.Vars(r)["cluster_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, h)
	}
}

// Heartbeats handles GET /api/v1/clusters/{cluster_id}/heartbeats.
func Heartbeats(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hb, err := st.GetHeartbeats(r.Context(), mux.Vars(r)["cluster_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, hb)
	}
}

type bootstrapBody struct {
	ServerIDs []string `json:"server_ids"`
}

// Bootstrap handles POST /api/v1/clusters/{cluster_id}/bootstrap. Servers
// must already be registered (via POST .../servers, which is how
// ClusterServer rows are created — ClusterStore exposes no bulk-create); this
// endpoint validates that every listed server_id is a member of the cluster
// and acknowledges the cluster as ready for election/heartbeating to begin,
// which the HA manager (server/internal/ha, wired in by the integration
// layer) picks up from the database independently.
func Bootstrap(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusterID := mux.Vars(r)["cluster_id"]
		if _, err := st.GetClusterByID(r.Context(), clusterID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body bootstrapBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if len(body.ServerIDs) == 0 {
			httpx.RespondError(w, r, log, httpx.Invalid("server_ids must contain at least one server", "field", "server_ids"))
			return
		}

		members, err := st.ListServers(r.Context(), clusterID)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		known := make(map[string]bool, len(members))
		for _, m := range members {
			known[m.ID] = true
		}
		for _, id := range body.ServerIDs {
			if !known[id] {
				httpx.RespondError(w, r, log, httpx.Invalid(fmt.Sprintf("server_id %q is not registered in this cluster", id), "field", "server_ids"))
				return
			}
		}

		httpx.RespondCreated(w, map[string]any{
			"message": "cluster bootstrapped",
			"servers": members,
		})
	}
}

func firstPositive(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
