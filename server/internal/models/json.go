package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
)

// IPNet wraps net.IPNet with JSON marshaling as a CIDR string ("10.1.0.0/24").
// net.IPNet does not itself implement encoding.TextMarshaler/Unmarshaler, so a
// bare *net.IPNet field would marshal as an object of its unexported-looking
// fields rather than a string; models use *IPNet wherever CONTRACTS.md Part 1
// specifies net.IPNet.
type IPNet net.IPNet

// String returns the CIDR notation, or "" for the zero value.
func (n IPNet) String() string {
	if n.IP == nil {
		return ""
	}
	ipnet := net.IPNet(n)
	return ipnet.String()
}

// MarshalJSON implements json.Marshaler.
func (n IPNet) MarshalJSON() ([]byte, error) {
	if n.IP == nil {
		return []byte("null"), nil
	}
	return json.Marshal(n.String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (n *IPNet) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*n = IPNet{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("models: IPNet must be a JSON string: %w", err)
	}
	if s == "" {
		*n = IPNet{}
		return nil
	}
	_, parsed, err := net.ParseCIDR(s)
	if err != nil {
		return fmt.Errorf("models: invalid CIDR %q: %w", s, err)
	}
	*n = IPNet(*parsed)
	return nil
}

// ParseIPNet parses a CIDR string into an *IPNet, returning nil for an empty
// string. It is the constructor counterpart to IPNet.String/MarshalJSON.
func ParseIPNet(s string) (*IPNet, error) {
	if s == "" {
		return nil, nil
	}
	_, parsed, err := net.ParseCIDR(s)
	if err != nil {
		return nil, fmt.Errorf("models: invalid CIDR %q: %w", s, err)
	}
	n := IPNet(*parsed)
	return &n, nil
}

// HardwareAddr wraps net.HardwareAddr with JSON marshaling as a MAC address
// string ("aa:bb:cc:dd:ee:ff"). net.HardwareAddr does not implement
// encoding.TextMarshaler/Unmarshaler, so a bare net.HardwareAddr field would
// marshal as a base64 byte array; models use HardwareAddr wherever
// CONTRACTS.md Part 1 specifies net.HardwareAddr.
type HardwareAddr net.HardwareAddr

// String returns the colon-separated MAC notation, or "" for nil/empty.
func (h HardwareAddr) String() string {
	if len(h) == 0 {
		return ""
	}
	return net.HardwareAddr(h).String()
}

// MarshalJSON implements json.Marshaler.
func (h HardwareAddr) MarshalJSON() ([]byte, error) {
	if len(h) == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(h.String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (h *HardwareAddr) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*h = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("models: HardwareAddr must be a JSON string: %w", err)
	}
	if s == "" {
		*h = nil
		return nil
	}
	mac, err := net.ParseMAC(s)
	if err != nil {
		return fmt.Errorf("models: invalid MAC address %q: %w", s, err)
	}
	*h = HardwareAddr(mac)
	return nil
}
