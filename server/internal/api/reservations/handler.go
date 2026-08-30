// Package reservations implements the Reservation Endpoints of
// docs/api/endpoints.md.
package reservations

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// mapReservationError translates store errors into human-readable API errors
// for the reservations endpoints. Amendment A19: the store's CreateReservation
// and UpdateReservation can fail with three sentinel errors that indicate
// specific constraint violations (client already reserved, address reserved for
// another client, or address leased to another client). The generic classify()
// path in httpx.RespondError would return the raw Postgres constraint name,
// which is unintelligible to an operator. This helper maps them to clear,
// actionable messages before the handler responds.
func mapReservationError(err error) *httpx.APIError {
	if errors.Is(err, models.ErrReservationClientExists) {
		return httpx.Conflict(
			"This client already has a reservation in this pool. Move that reservation to this address instead of adding a second one.",
			"reason", "client_already_reserved",
		)
	}
	if errors.Is(err, models.ErrReservationIPTaken) {
		return httpx.Conflict(
			"This address is already reserved for a different client in this pool.",
			"reason", "address_already_reserved",
		)
	}
	if errors.Is(err, models.ErrReservationIPLeased) {
		return httpx.Conflict(
			"This address is currently leased to a different client, so reserving it would leave that client unable to get an address. Release the lease first.",
			"reason", "address_actively_leased",
		)
	}
	// For any other error, fall through to the caller's default error handling.
	return nil
}

// canonicaliseBindingKey turns the binding_type plus the operator-supplied
// value into the canonical stored key. The API contract is that a client
// sends a RAW value ("aa:bb:cc:dd:ee:ff") and the server canonicalises it to
// "mac:aa:bb:cc:dd:ee:ff"; storing the raw value is what made every
// reservation ever created through this endpoint silently fail to match.
//
// An already-prefixed key is accepted unchanged (the CLI and older clients may
// send either) but is re-canonicalised so a stale upper-case MAC cannot slip
// through. Note the "is it already prefixed?" test must check for a *known
// type* prefix, not merely for a colon -- a bare MAC contains five colons, and
// treating those as a prefix is what made the endpoint reject raw MACs.
func canonicaliseBindingKey(bindingType, value string) (string, error) {
	t := models.BindingType(bindingType)
	if !t.Valid() {
		return "", httpx.Invalid(
			"binding_type must be one of "+strings.Join(models.BindingTypeStrings(), ", "),
			"field", "binding_type",
		)
	}
	if keyType, raw, ok := models.ParseBindingKey(value); ok {
		if keyType != t {
			return "", httpx.Invalid(
				"binding_key is already prefixed with a different type than binding_type",
				"field", "binding_key",
				"issue", "type_mismatch",
			)
		}
		value = raw
	}
	key, err := models.BuildBindingKey(t, value)
	if err != nil {
		return "", httpx.Invalid(err.Error(), "field", "binding_key")
	}
	return key, nil
}

// List handles GET /api/v1/pools/{pool_id}/reservations.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		poolID := mux.Vars(r)["pool_id"]
		params := httpx.ParseListParams(r)
		res, err := st.ListReservationsByPool(r.Context(), poolID, params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// Get handles GET /api/v1/reservations/{reservation_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := st.GetReservation(r.Context(), mux.Vars(r)["reservation_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, res)
	}
}

// createBody holds the request for POST /api/v1/pools/{pool_id}/reservations.
// The client sends binding_type (one of the seven types from models.BindingTypes)
// plus a raw binding value. The server canonicalises these into the prefixed key
// (e.g. "mac:aa:bb:cc:dd:ee:ff") before storing. If the client sends an already-
// prefixed key, it will be validated but not double-prefixed.
type createBody struct {
	BindingKey  string  `json:"binding_key"`  // Raw value or already-prefixed "type:value"
	BindingType string  `json:"binding_type"` // One of models.BindingTypes: mac, client_id, hostname, etc.
	ReservedIP  string  `json:"reserved_ip"`
	Hostname    *string `json:"hostname"`
	Description *string `json:"description"`
}

// Create handles POST /api/v1/pools/{pool_id}/reservations.
func Create(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		poolID := mux.Vars(r)["pool_id"]
		if _, err := st.GetPoolByID(r.Context(), poolID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body createBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("binding_key", body.BindingKey); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("binding_type", body.BindingType); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		// Canonicalise the binding_key: accept either a raw value or an
		// already-prefixed "type:value" form, but never double-prefix.
		canonicalKey, err := canonicaliseBindingKey(body.BindingType, body.BindingKey)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		ip, err := httpx.ParseIP("reserved_ip", body.ReservedIP)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		// BindingType always agrees with the key's prefix; take it from the
		// canonical key so the two can never drift apart.
		keyType, _, _ := models.ParseBindingKey(canonicalKey)

		res := &models.DHCPReservation{
			PoolID:      poolID,
			Hostname:    body.Hostname,
			Description: body.Description,
			ReservedIP:  ip,
			BindingKey:  canonicalKey,
			BindingType: keyType,
			IsEnabled:   true,
		}
		if err := st.CreateReservation(r.Context(), res); err != nil {
			// Amendment A19: map reservation conflicts to user-friendly messages
			// instead of leaking Postgres constraint names.
			if apiErr := mapReservationError(err); apiErr != nil {
				httpx.RespondError(w, r, log, apiErr)
				return
			}
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, res)
	}
}

// updateBody holds the request for PUT /api/v1/reservations/{reservation_id}.
// BindingKey and BindingType are optional; if provided, the canonicalised key
// is stored. If only BindingKey is provided, it's canonicalised against the
// reservation's existing BindingType. If both are provided, BindingType may
// change and BindingKey is canonicalised against the new type.
//
// ReservedIP is optional; amendment A19 allows moving a reservation to a
// different address within the same pool. When non-empty, it is parsed and
// validated like a create; when empty, the existing address is kept. The store
// re-validates that the new address is within the pool range and not taken by
// another reservation or active lease.
type updateBody struct {
	Hostname    *string `json:"hostname"`
	Description *string `json:"description"`
	IsEnabled   *bool   `json:"is_enabled"`
	ReservedIP  string  `json:"reserved_ip"`  // Optional; move the reservation to this address
	BindingKey  string  `json:"binding_key"`  // Optional; raw value or already-prefixed "type:value"
	BindingType string  `json:"binding_type"` // Optional; if provided with BindingKey, the type changes
}

// Update handles PUT /api/v1/reservations/{reservation_id}.
func Update(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["reservation_id"]
		existing, err := st.GetReservation(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		// Amendment A19: the enable/disable toggle in the pool Reservations tab
		// sends only {"is_enabled": bool}. With direct assignment of body.Hostname
		// and body.Description, omitted JSON keys would set the stored values to nil,
		// wiping them. Instead, we read the body once and decode it into both a
		// map (to check key presence) and a struct (to get typed values): an absent
		// key leaves the stored value alone, while an explicit null still clears it.
		// This matches REST semantics where a PUT with partial JSON is a targeted
		// update, not a full replace (see RFC 5789 PATCH vs RFC 7231 PUT for the
		// philosophical underpinning, even though we use PUT not PATCH).
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			httpx.RespondError(w, r, log, httpx.Invalid("failed to read request body"))
			return
		}

		var body updateBody
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			httpx.RespondError(w, r, log, httpx.Invalid("malformed JSON body"))
			return
		}

		var rawBody map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &rawBody); err != nil {
			httpx.RespondError(w, r, log, httpx.Invalid("malformed JSON body"))
			return
		}

		// Only update fields that are explicitly present in the JSON.
		if _, ok := rawBody["hostname"]; ok {
			existing.Hostname = body.Hostname
		}
		if _, ok := rawBody["description"]; ok {
			existing.Description = body.Description
		}
		if body.IsEnabled != nil {
			existing.IsEnabled = *body.IsEnabled
		}

		// Handle reserved_ip update: amendment A19 allows moving a reservation
		// to a different address. Parse and validate it like create does.
		if body.ReservedIP != "" {
			ip, err := httpx.ParseIP("reserved_ip", body.ReservedIP)
			if err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
			existing.ReservedIP = ip
		}

		// Handle binding key/type updates. If either is provided, canonicalise.
		if body.BindingKey != "" || body.BindingType != "" {
			// Use the new type if provided; otherwise use the existing type
			typeToUse := string(existing.BindingType)
			if body.BindingType != "" {
				typeToUse = body.BindingType
			}

			// Use the new key if provided; otherwise use the existing key
			valueToUse := body.BindingKey
			if valueToUse == "" {
				// Only the type is changing: reuse the existing key's value.
				// A legacy row written before migration 0005 has no type
				// prefix, in which case its whole key is the value.
				if _, keyValue, ok := models.ParseBindingKey(existing.BindingKey); ok {
					valueToUse = keyValue
				} else {
					valueToUse = existing.BindingKey
				}
			}

			// Canonicalise
			canonicalKey, err := canonicaliseBindingKey(typeToUse, valueToUse)
			if err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}

			existing.BindingKey = canonicalKey
			keyType, _, _ := models.ParseBindingKey(canonicalKey)
			existing.BindingType = keyType
		}

		if err := st.UpdateReservation(r.Context(), existing); err != nil {
			// Amendment A19: map reservation conflicts to user-friendly messages.
			if apiErr := mapReservationError(err); apiErr != nil {
				httpx.RespondError(w, r, log, apiErr)
				return
			}
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// Delete handles DELETE /api/v1/reservations/{reservation_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteReservation(r.Context(), mux.Vars(r)["reservation_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "reservation deleted")
	}
}
