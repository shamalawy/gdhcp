package ipam

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 200
)

// Search handles GET /api/v1/search?q&entity_types&limit: global substring
// search across every entity type SearchStore knows about.
func Search(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if err := httpx.RequireString("q", q); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var entityTypes []string
		if raw := r.URL.Query().Get("entity_types"); raw != "" {
			for _, t := range strings.Split(raw, ",") {
				if t = strings.TrimSpace(t); t != "" {
					entityTypes = append(entityTypes, t)
				}
			}
		}

		limit := httpx.ParseIntQuery(r, "limit", defaultSearchLimit)
		if limit <= 0 || limit > maxSearchLimit {
			limit = defaultSearchLimit
		}

		results, err := st.GlobalSearch(r.Context(), q, entityTypes, limit)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, results)
	}
}
