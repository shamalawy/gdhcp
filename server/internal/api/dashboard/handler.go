// Package dashboard implements the Dashboard Endpoints of
// docs/api/endpoints.md (stats, utilization-chart, dhcp-stats) plus
// GET /api/v1/events, the SSE live-update stream from amendment A12.
package dashboard

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

const defaultStatsWindow = time.Hour

// Stats handles GET /api/v1/dashboard/stats?window.
func Stats(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		window := httpx.ParseWindow(r, defaultStatsWindow)
		s, err := st.GetDashboardStats(r.Context(), window)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, s)
	}
}

const defaultChartDays = 7

// UtilizationChart handles
// GET /api/v1/dashboard/utilization-chart?entity_type&entity_id&days.
func UtilizationChart(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityType := models.UtilizationEntityType(r.URL.Query().Get("entity_type"))
		switch entityType {
		case models.UtilizationEntityScope, models.UtilizationEntityPool, models.UtilizationEntityVRF, models.UtilizationEntityPrefix:
		default:
			httpx.RespondError(w, r, log, httpx.Invalid("entity_type must be one of scope, pool, vrf, prefix", "field", "entity_type"))
			return
		}
		entityID := r.URL.Query().Get("entity_id")
		if err := httpx.RequireString("entity_id", entityID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		days := httpx.ParseIntQuery(r, "days", defaultChartDays)
		if days <= 0 {
			days = defaultChartDays
		}

		points, err := st.GetUtilizationChart(r.Context(), entityType, entityID, days)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, points)
	}
}

const defaultDHCPStatsWindow = 24 * time.Hour

// DHCPStats handles GET /api/v1/dashboard/dhcp-stats?scope_id&window.
// scope_id is required: StatsRecorder aggregates per scope, and the store
// surface has no all-scopes rollup beyond DashboardStats's totals — a
// stricter requirement than docs/api/endpoints.md's query-param table marks,
// noted in notes_for_integration.
func DHCPStats(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scopeID := r.URL.Query().Get("scope_id")
		if err := httpx.RequireString("scope_id", scopeID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		window := httpx.ParseWindow(r, defaultDHCPStatsWindow)
		stats, err := st.GetStats(r.Context(), scopeID, window)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, stats)
	}
}
