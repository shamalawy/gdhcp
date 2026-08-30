package models

import (
	"errors"
	"testing"
)

func TestBuildBindingKeyCanonicalises(t *testing.T) {
	tests := []struct {
		name string
		typ  BindingType
		raw  string
		want string
	}{
		// A MAC must reach the database in exactly the form
		// net.HardwareAddr.String() produces, because that is what the engine
		// builds its lookup candidates from. Any other spelling silently
		// never matches.
		{"mac lowercased", BindingTypeMAC, "AA:BB:CC:DD:EE:FF", "mac:aa:bb:cc:dd:ee:ff"},
		{"mac dash form", BindingTypeMAC, "aa-bb-cc-dd-ee-ff", "mac:aa:bb:cc:dd:ee:ff"},
		{"mac already canonical", BindingTypeMAC, "aa:bb:cc:dd:ee:ff", "mac:aa:bb:cc:dd:ee:ff"},
		{"mac padded", BindingTypeMAC, "  aa:bb:cc:dd:ee:ff  ", "mac:aa:bb:cc:dd:ee:ff"},

		{"hostname lowercased", BindingTypeHostname, "Web-SRV-01", "hostname:web-srv-01"},

		// Opaque identifiers are stored as lowercase hex. A value that already
		// looks like hex passes through; anything else is hex-encoded, so an
		// operator can type a readable circuit ID.
		{"client_id hex passthrough", BindingTypeClientID, "01AABBCC", "client_id:01aabbcc"},
		{"circuit_id from text", BindingTypeCircuitID, "eth 1/0/24", "circuit_id:65746820312f302f3234"},
		{"vendor_class from text", BindingTypeVendorClass, "MSFT 5.0", "vendor_class:4d53465420352e30"},
		{"user_class odd length is text", BindingTypeUserClass, "abc", "user_class:616263"},
		{"remote_id even hex", BindingTypeRemoteID, "DEAD", "remote_id:dead"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildBindingKey(tc.typ, tc.raw)
			if err != nil {
				t.Fatalf("BuildBindingKey(%q, %q): %v", tc.typ, tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("BuildBindingKey(%q, %q) = %q, want %q", tc.typ, tc.raw, got, tc.want)
			}
			if !IsCanonicalBindingKey(got) {
				t.Errorf("BuildBindingKey produced non-canonical key %q", got)
			}
		})
	}
}

func TestBuildBindingKeyRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		typ  BindingType
		raw  string
	}{
		{"unknown type", BindingType("duid"), "abcd"},
		{"empty value", BindingTypeMAC, "   "},
		{"malformed mac", BindingTypeMAC, "not-a-mac"},
		{"short mac", BindingTypeMAC, "aa:bb:cc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildBindingKey(tc.typ, tc.raw); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("BuildBindingKey(%q, %q) error = %v, want ErrInvalidInput", tc.typ, tc.raw, err)
			}
		})
	}
}

func TestBuildBindingKeyRoundTrips(t *testing.T) {
	for _, typ := range BindingTypePrecedence {
		raw := "aa:bb:cc:dd:ee:ff"
		if typ != BindingTypeMAC {
			raw = "abcd"
		}
		key, err := BuildBindingKey(typ, raw)
		if err != nil {
			t.Fatalf("BuildBindingKey(%q): %v", typ, err)
		}
		gotType, gotValue, ok := ParseBindingKey(key)
		if !ok {
			t.Fatalf("ParseBindingKey(%q) failed to parse a key we just built", key)
		}
		if gotType != typ {
			t.Errorf("ParseBindingKey(%q) type = %q, want %q", key, gotType, typ)
		}
		if rebuilt := string(gotType) + ":" + gotValue; rebuilt != key {
			t.Errorf("round-trip of %q produced %q", key, rebuilt)
		}
	}
}

// A bare MAC contains colons but no type prefix. Treating "contains a colon"
// as "is prefixed" is what made the API reject raw MAC addresses outright, so
// pin the distinction down.
func TestParseBindingKeyRejectsUnprefixedValues(t *testing.T) {
	for _, key := range []string{
		"aa:bb:cc:dd:ee:ff", // legacy unprefixed MAC
		"abcd",              // legacy unprefixed opaque value
		"duid:abcd",         // unknown type
		"mac:",              // missing value
		":abcd",             // missing type
		"",
	} {
		if _, _, ok := ParseBindingKey(key); ok {
			t.Errorf("ParseBindingKey(%q) reported a valid prefix; want none", key)
		}
		if HasBindingTypePrefix(key) {
			t.Errorf("HasBindingTypePrefix(%q) = true, want false", key)
		}
	}
}

func TestIsCanonicalBindingKeyRejectsNonCanonicalValues(t *testing.T) {
	// Correctly prefixed but the value is not in canonical form: the engine
	// would never match it, so the store must refuse it.
	for _, key := range []string{
		"mac:AA:BB:CC:DD:EE:FF",
		"mac:not-a-mac",
		"hostname:Web-SRV",
		"client_id:ABCD",
	} {
		if IsCanonicalBindingKey(key) {
			t.Errorf("IsCanonicalBindingKey(%q) = true, want false", key)
		}
	}
	for _, key := range []string{
		"mac:aa:bb:cc:dd:ee:ff",
		"hostname:web-srv",
		"client_id:abcd",
		"circuit_id:65746830",
	} {
		if !IsCanonicalBindingKey(key) {
			t.Errorf("IsCanonicalBindingKey(%q) = false, want true", key)
		}
	}
}

func TestClientIdentityBindingKeysPrecedence(t *testing.T) {
	id := ClientIdentity{
		MAC:         "aa:bb:cc:dd:ee:ff",
		ClientID:    "0102",
		Hostname:    "printer",
		CircuitID:   "aabb",
		RemoteID:    "ccdd",
		VendorClass: "eeff",
		UserClass:   "1122",
	}
	want := []string{
		"client_id:0102",
		"mac:aa:bb:cc:dd:ee:ff",
		"circuit_id:aabb",
		"remote_id:ccdd",
		"vendor_class:eeff",
		"user_class:1122",
		"hostname:printer",
	}
	got := id.BindingKeys()
	if len(got) != len(want) {
		t.Fatalf("BindingKeys() returned %d keys, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("BindingKeys()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestClientIdentityBindingKeysSkipsAbsentIdentifiers(t *testing.T) {
	id := ClientIdentity{MAC: "aa:bb:cc:dd:ee:ff"}
	got := id.BindingKeys()
	if len(got) != 1 || got[0] != "mac:aa:bb:cc:dd:ee:ff" {
		t.Errorf("BindingKeys() = %v, want just the MAC key", got)
	}
	if (ClientIdentity{}).IsZero() != true {
		t.Error("empty ClientIdentity should report IsZero")
	}
	if id.IsZero() {
		t.Error("identity with a MAC should not report IsZero")
	}
}

func TestBindingTypeRankMatchesPrecedence(t *testing.T) {
	for i, typ := range BindingTypePrecedence {
		if !typ.Valid() {
			t.Errorf("%q is in the precedence list but reports invalid", typ)
		}
		if got := typ.Rank(); got != i {
			t.Errorf("%q.Rank() = %d, want %d", typ, got, i)
		}
	}
	unknown := BindingType("duid")
	if unknown.Valid() {
		t.Error("unknown binding type reported valid")
	}
	if got := unknown.Rank(); got != len(BindingTypePrecedence) {
		t.Errorf("unknown Rank() = %d, want %d (sorts last)", got, len(BindingTypePrecedence))
	}
}
