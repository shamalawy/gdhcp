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
