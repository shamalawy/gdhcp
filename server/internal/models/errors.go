// Package models contains the domain entities shared across gdhcp: the DHCP/IPAM
// object model, users and auth artifacts, HA cluster state, and the sentinel
// errors every store/service layer maps its failures onto.
package models

import "errors"

// Sentinel domain errors. Store and service implementations should wrap these
// with fmt.Errorf("...: %w", ErrX) to preserve context while remaining
// detectable via errors.Is. The API layer maps these to the canonical error
// codes in docs/CONTRACTS.md Part 6.
var (
	// ErrNotFound indicates the requested entity does not exist.
	// Maps to HTTP 404 / NOT_FOUND.
	ErrNotFound = errors.New("not found")

	// ErrConflict indicates a uniqueness or overlap constraint was violated
	// (duplicate name, overlapping CIDR, duplicate binding key, etc.).
	// Maps to HTTP 409 / CONFLICT.
	ErrConflict = errors.New("conflict")

	// ErrNoLeases indicates a pool has no addresses available for allocation
	// after exhausting the allocation retry loop (amendment A6).
	// Maps to HTTP 409 / CONFLICT (or triggers a DHCPNAK in the DHCP engine).
	ErrNoLeases = errors.New("no leases available in pool")

	// ErrLeaseNotFound indicates a lease renewal/release referenced a lease
	// that does not exist, is expired, or whose client MAC does not match.
	// Maps to HTTP 404 / NOT_FOUND.
	ErrLeaseNotFound = errors.New("lease not found")

	// ErrUnauthorized indicates missing or invalid authentication credentials.
	// Maps to HTTP 401 / UNAUTHORIZED.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden indicates the authenticated principal lacks the role
	// required for the action (see amendment A15 RBAC roles).
	// Maps to HTTP 403 / FORBIDDEN.
	ErrForbidden = errors.New("forbidden")

	// ErrInvalidInput indicates malformed request input (bad body, bad query
	// parameter, failed validation). Maps to HTTP 400 / INVALID_INPUT.
	ErrInvalidInput = errors.New("invalid input")

	// ErrServiceUnavailable indicates a required dependency (typically the
	// database) is unreachable. Maps to HTTP 503 / SERVICE_UNAVAILABLE.
	ErrServiceUnavailable = errors.New("service unavailable")

	// ErrHasDependents indicates a delete was rejected because dependent
	// child entities still reference this one (e.g. deleting a VRF that
	// still has scopes, or a prefix that still has children).
	// Maps to HTTP 409 / CONFLICT.
	ErrHasDependents = errors.New("entity has dependent records")
)

// Reservation conflicts, amendment A19. These are the three ways a
// reservation can collide with one that already exists. Each is wrapped
// alongside ErrConflict, so the generic 409 mapping still applies while the
// reservations handler can recognise the specific case and answer with a
// sentence an operator can act on.
//
// They exist because the generic path produced this, verbatim, in the UI's
// error toast:
//
//	store: create reservation: duplicate key value violates unique
//	constraint "dhcp_reservations_pool_id_binding_key_key": conflict
//
// which names a database constraint rather than the thing the operator did.
var (
	// ErrReservationClientExists indicates the client already holds a
	// reservation in this pool. UNIQUE (pool_id, binding_key) permits one per
	// client, so the existing reservation must be moved, not duplicated.
	ErrReservationClientExists = errors.New("client already has a reservation in this pool")

	// ErrReservationIPTaken indicates the address is already reserved for a
	// different client in this pool. Two reservations on one address would
	// leave whichever client asked second permanently unable to get it.
	ErrReservationIPTaken = errors.New("address is already reserved for another client in this pool")

	// ErrReservationIPLeased indicates the address currently has an active
	// lease held by a different client. Reserving it would starve that client
	// on every future request (amendment A6 step 1 wins unconditionally).
	ErrReservationIPLeased = errors.New("address is actively leased to a different client")
)
