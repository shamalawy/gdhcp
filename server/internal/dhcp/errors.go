// Package dhcp implements gdhcp's DHCPv4 engine: per-interface listeners,
// the DORA/DRAD state machine, VRF/relay-aware scope selection, HA-safe
// lease allocation, option inheritance resolution, lease lifecycle
// management, and transaction statistics/logging.
//
// See docs/design/dhcp-engine.md for the background design and
// docs/AMENDMENTS.md (binding, overrides the design doc where they
// conflict) for the allocation algorithm (A6), option value types (A5.4),
// T1/T2 timing, and the ServingGate consumer interface (A10).
package dhcp

import (
	"errors"
	"fmt"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

// Sentinel errors specific to DHCP protocol handling. Allocation/lease
// errors that originate in the store layer are the models.Err* sentinels
// (models.ErrNoLeases, models.ErrLeaseNotFound, etc.) — these are wrapped
// with %w so both sets remain detectable via errors.Is.
var (
	// ErrIPNotInScope indicates a client's requested IP (option 50) does not
	// fall within the network of any scope this server could serve.
	ErrIPNotInScope = errors.New("dhcp: requested ip not in any known scope")

	// ErrScopeNotFound indicates no scope could be identified for a request
	// (VRF/relay/interface selection all failed to match).
	ErrScopeNotFound = errors.New("dhcp: no scope found for request")

	// ErrInvalidRequest indicates a DHCP message did not carry the fields
	// required to classify it into a known DORA sub-state (RFC 2131 §4.3.2).
	ErrInvalidRequest = errors.New("dhcp: invalid or unclassifiable request")

	// ErrIPConflict indicates the IP the client selected/requested does not
	// match what the allocator is prepared to confirm (offer expired, lost
	// an allocation race, or ping-check found it already in use).
	ErrIPConflict = errors.New("dhcp: ip address unavailable or in use")

	// ErrRelayNotSupported indicates a relayed request's giaddr does not map
	// to any known scope (amendment: relay giaddr must match a configured
	// scope's network for the engine to serve it).
	ErrRelayNotSupported = errors.New("dhcp: relay giaddr does not map to a known scope")
)

// RequestError wraps a lower-level error with the DHCP message type and a
// short machine-readable reason, so dispatch's error path and logging.go can
// report structured detail without re-deriving it from the underlying error.
type RequestError struct {
	Err     error
	MsgType dhcpv4.MessageType
	Reason  string
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("dhcp: %s: %s: %v", e.MsgType, e.Reason, e.Err)
}

// Unwrap allows errors.Is/errors.As to see through to Err.
func (e *RequestError) Unwrap() error { return e.Err }
