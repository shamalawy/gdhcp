// actions.go implements the operator-driven lease mutations that sit
// alongside handler.go's read-only listing routes: release, abandon, force
// renew and delete, each available for one lease or for a selection of
// leases in a single bulk call.
//
// Release and abandon reuse the DHCP engine's own store methods, which
// match on the lease's client MAC; the handlers load the lease first and
// pass back its recorded MAC, so an operator acting from the UI does not
// have to supply an identifier the server already knows.
package leases

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// defaultRenewDuration backs a force-renew of a lease whose recorded
// start/end span is not usable (a zero or inverted window). It matches
// Postgres.AllocateLease's own fallback lease time.
const defaultRenewDuration = 24 * time.Hour

// maxBulkLeases caps one bulk request. Bulk actions run as N independent
// store calls, so an unbounded list would hold a request open for as long
// as the client cared to make it.
const maxBulkLeases = 500

// renewRequest is the optional body of a renew call. Omit it (or send
// duration_seconds: 0) to reuse the lease's own original duration.
type renewRequest struct {
	DurationSeconds int64 `json:"duration_seconds"`
}

// bulkRequest is the body of POST /leases/bulk.
type bulkRequest struct {
	Action          string   `json:"action"`
	LeaseIDs        []string `json:"lease_ids"`
	DurationSeconds int64    `json:"duration_seconds"`
}

// bulkResult reports one lease's outcome. Errors are per-lease rather than
// fatal to the whole request: releasing 40 leases where one has meanwhile
// been deleted should release the other 39 and say so, not roll back.
type bulkResult struct {
	LeaseID string `json:"lease_id"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

type bulkResponse struct {
	Action    string       `json:"action"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	Results   []bulkResult `json:"results"`
}

// leaseMAC returns the MAC recorded on the lease, or "" when the lease was
// bound by client-ID alone — which is what ReleaseLease/AbandonLease's
// `IS NOT DISTINCT FROM` comparison expects for a NULL client_mac.
func leaseMAC(l *models.DHCPLease) string {
	if l == nil || l.ClientMAC == nil {
		return ""
	}
	return l.ClientMAC.String()
}

// renewExpiry computes the new lease end for a force renew: the caller's
// explicit duration if given, else the lease's own original span, else the
// 24h fallback.
func renewExpiry(l *models.DHCPLease, durationSeconds int64) time.Time {
	d := time.Duration(durationSeconds) * time.Second
	if d <= 0 {
		d = l.LeaseEnd.Sub(l.LeaseStart)
	}
	if d <= 0 {
		d = defaultRenewDuration
	}
	return time.Now().UTC().Add(d)
}

// applyRelease marks the lease released so its address returns to the pool.
func applyRelease(r *http.Request, st store.Store, leaseID string) error {
	l, err := st.GetLease(r.Context(), leaseID)
	if err != nil {
		return err
	}
	return st.ReleaseLease(r.Context(), leaseID, leaseMAC(l))
}

// applyAbandon marks the lease abandoned: the address is treated as in use
// on the wire and is held back from reallocation until the reaper sweeps it.
func applyAbandon(r *http.Request, st store.Store, leaseID string) error {
	l, err := st.GetLease(r.Context(), leaseID)
	if err != nil {
		return err
	}
	return st.AbandonLease(r.Context(), leaseID, leaseMAC(l))
}

// applyRenew force-renews the lease, returning the updated row.
func applyRenew(r *http.Request, st store.Store, leaseID string, durationSeconds int64) (*models.DHCPLease, error) {
	l, err := st.GetLease(r.Context(), leaseID)
	if err != nil {
		return nil, err
	}
	return st.ForceRenewLease(r.Context(), leaseID, renewExpiry(l, durationSeconds))
}

// Release handles POST /api/v1/leases/{lease_id}/release.
func Release(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := applyRelease(r, st, mux.Vars(r)["lease_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "lease released")
	}
}

// Abandon handles POST /api/v1/leases/{lease_id}/abandon.
func Abandon(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := applyAbandon(r, st, mux.Vars(r)["lease_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "lease abandoned")
	}
}

// Renew handles POST /api/v1/leases/{lease_id}/renew. The body is optional.
// Amendment A19: models.DHCPLease.ReservationState and Reservation are
// populated by the store's join with dhcp_reservations and are ready to
// serialize; no handler-level work is needed to make them appear in the
// JSON response.
func Renew(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body renewRequest
		if r.ContentLength > 0 {
			if err := httpx.DecodeJSON(r, &body); err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
		}
		if body.DurationSeconds < 0 {
			httpx.RespondError(w, r, log, httpx.Invalid("duration_seconds must not be negative",
				"field", "duration_seconds", "issue", "invalid_range"))
			return
		}
		l, err := applyRenew(r, st, mux.Vars(r)["lease_id"], body.DurationSeconds)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, l)
	}
}

// Delete handles DELETE /api/v1/leases/{lease_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteLease(r.Context(), mux.Vars(r)["lease_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "lease deleted")
	}
}

// Bulk handles POST /api/v1/leases/bulk: one action applied to a selection
// of leases. It always answers 200 with a per-lease breakdown as long as
// the request itself was well-formed, so a partially-successful bulk action
// is reportable rather than being flattened into a single error.
func Bulk(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body bulkRequest
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		switch body.Action {
		case "release", "abandon", "renew", "delete":
		default:
			httpx.RespondError(w, r, log, httpx.Invalid(
				`action must be one of "release", "abandon", "renew", "delete"`,
				"field", "action", "issue", "invalid_value"))
			return
		}
		if len(body.LeaseIDs) == 0 {
			httpx.RespondError(w, r, log, httpx.Invalid("lease_ids must not be empty",
				"field", "lease_ids", "issue", "required"))
			return
		}
		if len(body.LeaseIDs) > maxBulkLeases {
			httpx.RespondError(w, r, log, httpx.Invalid("too many lease_ids in one request",
				"field", "lease_ids", "issue", "too_many"))
			return
		}
		if body.DurationSeconds < 0 {
			httpx.RespondError(w, r, log, httpx.Invalid("duration_seconds must not be negative",
				"field", "duration_seconds", "issue", "invalid_range"))
			return
		}

		resp := bulkResponse{Action: body.Action, Results: make([]bulkResult, 0, len(body.LeaseIDs))}
		for _, id := range body.LeaseIDs {
			var err error
			switch body.Action {
			case "release":
				err = applyRelease(r, st, id)
			case "abandon":
				err = applyAbandon(r, st, id)
			case "renew":
				_, err = applyRenew(r, st, id, body.DurationSeconds)
			case "delete":
				err = st.DeleteLease(r.Context(), id)
			}
			if err != nil {
				msg := bulkErrorMessage(err)
				if msg == genericBulkError && log != nil {
					// Unexpected failures are invisible to the client by
					// design (the per-lease message is deliberately
					// generic), so they have to be logged here — the
					// handler answers 200 and never reaches RespondError.
					log.Error("api: bulk lease action failed", "error", err, "action", body.Action,
						"lease_id", id, "trace_id", httpx.TraceID(r))
				}
				resp.Failed++
				resp.Results = append(resp.Results, bulkResult{LeaseID: id, Error: msg})
				continue
			}
			resp.Succeeded++
			resp.Results = append(resp.Results, bulkResult{LeaseID: id, OK: true})
		}
		httpx.RespondOK(w, resp)
	}
}

// bulkErrorMessage keeps per-lease failures readable without leaking store
// internals: the known sentinels get a plain sentence, anything else is
// reported generically (the handler's caller logs nothing per-item, so the
// router's recover/logging middleware is unaffected).
func bulkErrorMessage(err error) string {
	switch {
	case errors.Is(err, models.ErrLeaseNotFound), errors.Is(err, models.ErrNotFound):
		return "lease not found"
	case errors.Is(err, models.ErrInvalidInput):
		return "invalid request for this lease"
	case errors.Is(err, models.ErrConflict):
		return "conflicts with the lease's current state"
	default:
		return genericBulkError
	}
}

// genericBulkError is the per-lease message for anything unclassified; Bulk
// logs the real error server-side when it sees this.
const genericBulkError = "internal error"
