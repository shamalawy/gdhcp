package models

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"
)

// BindingType enumerates how a DHCPReservation identifies its client. Each
// type names a distinct identifier the DHCP engine can actually extract from
// an incoming packet -- a type the engine cannot match is worse than useless,
// since the operator gets a reservation that silently never fires (which is
// exactly what the original 'hostname' type did before option 12 was read).
type BindingType string

const (
	// BindingTypeMAC matches the packet's chaddr (client hardware address).
	BindingTypeMAC BindingType = "mac"
	// BindingTypeClientID matches option 61 (Client Identifier).
	BindingTypeClientID BindingType = "client_id"
	// BindingTypeHostname matches option 12 (Host Name).
	BindingTypeHostname BindingType = "hostname"
	// BindingTypeCircuitID matches option 82 sub-option 1 (Agent Circuit ID),
	// inserted by the relay -- typically the switch port the client is on.
	BindingTypeCircuitID BindingType = "circuit_id"
	// BindingTypeRemoteID matches option 82 sub-option 2 (Agent Remote ID),
	// inserted by the relay -- typically identifying the relay itself.
	BindingTypeRemoteID BindingType = "remote_id"
	// BindingTypeVendorClass matches option 60 (Vendor Class Identifier).
	BindingTypeVendorClass BindingType = "vendor_class"
	// BindingTypeUserClass matches option 77 (User Class).
	BindingTypeUserClass BindingType = "user_class"
)

// BindingTypePrecedence lists every binding type in MATCH PRECEDENCE order,
// most specific first. When a client's identity matches several reservations
// in the same pool, the earliest type in this list wins.
//
// The ordering is deliberate: an explicit client identifier (RFC 2131 / RFC
// 4361 option 61) is the client's own stable identity and outranks the
// hardware address, which can move between NICs. Relay-inserted identifiers
// (option 82) come next -- they are asserted by trusted network equipment
// rather than by the client. Class identifiers describe a *kind* of device
// rather than one device, so they are weaker still. A hostname is asserted by
// the client itself, is trivially spoofed and is not unique, so it must never
// outrank a hardware or identifier match.
//
// Postgres (via array_position over this list) and the in-memory mock both
// order candidate reservations by this slice, so the two agree exactly.
var BindingTypePrecedence = []BindingType{
	BindingTypeClientID,
	BindingTypeMAC,
	BindingTypeCircuitID,
	BindingTypeRemoteID,
	BindingTypeVendorClass,
	BindingTypeUserClass,
	BindingTypeHostname,
}

// BindingTypeStrings returns BindingTypePrecedence as plain strings, for
// passing to SQL as a text[] parameter.
func BindingTypeStrings() []string {
	out := make([]string, len(BindingTypePrecedence))
	for i, t := range BindingTypePrecedence {
		out[i] = string(t)
	}
	return out
}

// Valid reports whether t is a known binding type.
func (t BindingType) Valid() bool {
	for _, known := range BindingTypePrecedence {
		if t == known {
			return true
		}
	}
	return false
}

// Rank returns t's position in BindingTypePrecedence (lower binds tighter),
// or len(BindingTypePrecedence) for an unknown type so it sorts last.
func (t BindingType) Rank() int {
	for i, known := range BindingTypePrecedence {
		if t == known {
			return i
		}
	}
	return len(BindingTypePrecedence)
}

// opaque reports whether values of this type are arbitrary byte strings that
// are canonicalised to lowercase hex (as opposed to a MAC or a hostname,
// which have their own textual canonical forms).
func (t BindingType) opaque() bool {
	switch t {
	case BindingTypeClientID, BindingTypeCircuitID, BindingTypeRemoteID,
		BindingTypeVendorClass, BindingTypeUserClass:
		return true
	default:
		return false
	}
}

// ClientIdentity holds every identifying attribute a DHCP client presented in
// one request, already canonicalised into the exact string forms the stored
// binding keys use. The DHCP engine builds one of these per packet and the
// store matches reservations against it.
//
// It replaces the (clientMAC, clientID) string pair the allocation path used
// to carry: threading seven identifiers through as positional arguments would
// be unreadable, and every new identifier would break the signature again.
type ClientIdentity struct {
	MAC         string // chaddr, lowercase colon form ("aa:bb:cc:dd:ee:ff")
	ClientID    string // option 61, lowercase hex
	Hostname    string // option 12, trimmed and lowercased
	CircuitID   string // option 82 sub-option 1, lowercase hex
	RemoteID    string // option 82 sub-option 2, lowercase hex
	VendorClass string // option 60, lowercase hex
	UserClass   string // option 77, lowercase hex
}

// valueFor returns the identity's canonical value for t, or "" if the client
// did not present that identifier.
func (c ClientIdentity) valueFor(t BindingType) string {
	switch t {
	case BindingTypeMAC:
		return c.MAC
	case BindingTypeClientID:
		return c.ClientID
	case BindingTypeHostname:
		return c.Hostname
	case BindingTypeCircuitID:
		return c.CircuitID
	case BindingTypeRemoteID:
		return c.RemoteID
	case BindingTypeVendorClass:
		return c.VendorClass
	case BindingTypeUserClass:
		return c.UserClass
	default:
		return ""
	}
}

// BindingKeys returns the reservation binding_key candidates identifying this
// client, in BindingTypePrecedence order, skipping identifiers the client did
// not present. The store matches a reservation by looking for any of these.
func (c ClientIdentity) BindingKeys() []string {
	keys := make([]string, 0, len(BindingTypePrecedence))
	for _, t := range BindingTypePrecedence {
		if v := c.valueFor(t); v != "" {
			keys = append(keys, string(t)+":"+v)
		}
	}
	return keys
}

// IsZero reports whether the client presented no usable identifier at all.
func (c ClientIdentity) IsZero() bool { return len(c.BindingKeys()) == 0 }

// isHexString reports whether s is a plausible hex encoding: a non-empty even
// number of hex digits.
func isHexString(s string) bool {
	if s == "" || len(s)%2 != 0 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// CanonicalBindingValue normalises an operator-supplied identifier value into
// the exact form the DHCP engine produces from a packet. Getting this wrong is
// how reservations silently stop matching, so every write path must go through
// here.
//
// Per type:
//   - mac:      parsed with net.ParseMAC (so "AA-BB-CC-DD-EE-FF" and
//     "aabb.ccdd.eeff" are accepted) and rendered lowercase colon-separated.
//   - hostname: trimmed and lowercased.
//   - the opaque types (client_id, circuit_id, remote_id, vendor_class,
//     user_class): lowercase hex. A value that already looks like hex (an even
//     number of hex digits) is taken as hex and simply lowercased; anything
//     else is hex-encoded as its UTF-8 bytes, so an operator can type a
//     human-readable circuit ID such as "eth 1/0/24" directly.
//
// The hex-or-text rule is inherently ambiguous for short even-length inputs
// ("1234" is both valid hex and plausible text). Callers that know which they
// mean -- the web UI, which offers an explicit Text/Hex mode -- should send
// hex, which round-trips unchanged.
func CanonicalBindingValue(t BindingType, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("%w: binding value must not be empty", ErrInvalidInput)
	}
	switch {
	case t == BindingTypeMAC:
		hw, err := net.ParseMAC(v)
		if err != nil {
			return "", fmt.Errorf("%w: %q is not a valid MAC address", ErrInvalidInput, raw)
		}
		return hw.String(), nil
	case t == BindingTypeHostname:
		return strings.ToLower(v), nil
	case t.opaque():
		if isHexString(v) {
			return strings.ToLower(v), nil
		}
		return hex.EncodeToString([]byte(v)), nil
	default:
		return "", fmt.Errorf("%w: unknown binding type %q", ErrInvalidInput, string(t))
	}
}

// BuildBindingKey canonicalises raw and returns the stored binding_key form,
// "<type>:<canonical value>" -- for example ("mac", "AA:BB:CC:DD:EE:FF")
// yields "mac:aa:bb:cc:dd:ee:ff".
func BuildBindingKey(t BindingType, raw string) (string, error) {
	if !t.Valid() {
		return "", fmt.Errorf("%w: unknown binding type %q", ErrInvalidInput, string(t))
	}
	v, err := CanonicalBindingValue(t, raw)
	if err != nil {
		return "", err
	}
	return string(t) + ":" + v, nil
}

// ParseBindingKey splits a stored binding_key into its type and raw value.
// ok is false when key carries no recognised type prefix -- which is how the
// legacy unprefixed rows written before migration 0005 are detected.
func ParseBindingKey(key string) (t BindingType, value string, ok bool) {
	idx := strings.Index(key, ":")
	if idx <= 0 || idx == len(key)-1 {
		return "", "", false
	}
	candidate := BindingType(key[:idx])
	if !candidate.Valid() {
		return "", "", false
	}
	return candidate, key[idx+1:], true
}

// HasBindingTypePrefix reports whether key already begins with a recognised
// "<type>:" prefix.
//
// Note this cannot be approximated by "does the value contain a colon": a bare
// MAC address contains five of them. Testing for a colon was the bug that made
// the API reject raw MAC addresses outright.
func HasBindingTypePrefix(key string) bool {
	_, _, ok := ParseBindingKey(key)
	return ok
}

// IsCanonicalBindingKey reports whether key is both correctly prefixed and
// already in its canonical value form, so re-canonicalising it is a no-op.
// The store uses this as a last line of defence: a non-canonical key must
// never reach the database, because the engine would never match it.
func IsCanonicalBindingKey(key string) bool {
	t, value, ok := ParseBindingKey(key)
	if !ok {
		return false
	}
	canonical, err := CanonicalBindingValue(t, value)
	return err == nil && canonical == value
}

// ClientKind distinguishes the two row sources a DHCPClient can come from.
type ClientKind string

const (
	// ClientKindLease is a row backed by a dhcp_leases record.
	ClientKindLease ClientKind = "lease"
	// ClientKindReservation is a row backed by a dhcp_reservations record.
	ClientKindReservation ClientKind = "reservation"
)

// DHCPClient is one row of the unified client view behind
// GET /api/v1/prefixes/{prefix_id}/clients: a lease or a reservation, resolved
// up to the scope and IPAM prefix it belongs to so the IPAM section can show
// who is actually on an address range without the operator having to walk down
// to each pool.
//
// Fields that only make sense for one Kind are pointers and are null for the
// other: a reservation has no lease timing or binding state, and a lease has
// no binding key.
type DHCPClient struct {
	ID             string        `json:"id"`
	Kind           ClientKind    `json:"kind"`
	IPAddress      net.IP        `json:"ip_address"`
	PoolID         string        `json:"pool_id"`
	PoolName       string        `json:"pool_name"`
	ScopeID        string        `json:"scope_id"`
	ScopeName      string        `json:"scope_name"`
	PrefixID       string        `json:"prefix_id"`
	ClientMAC      *HardwareAddr `json:"client_mac"`
	ClientID       []byte        `json:"client_id,omitempty"`
	ClientHostname *string       `json:"client_hostname"`

	// Lease-only fields.
	BindingState *LeaseBindingState `json:"binding_state"`
	LeaseStart   *time.Time         `json:"lease_start"`
	LeaseEnd     *time.Time         `json:"lease_end"`

	// Reservation-only fields.
	BindingKey  *string      `json:"binding_key"`
	BindingType *BindingType `json:"binding_type"`
	IsEnabled   *bool        `json:"is_enabled"`

	// ReservationState and Reservation are amendment A19, carried here for
	// exactly the same reason DHCPLease carries them: this table and the DHCP
	// leases table must render the same pin state from the same field rather
	// than each inferring it differently. A lease row gets the reservation
	// that concerns it, if any; a reservation row describes itself, so it is
	// always "pinned" (or "pinned_disabled" when is_enabled is false).
	ReservationState LeaseReservationState `json:"reservation_state"`
	Reservation      *LeaseReservation     `json:"reservation"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
