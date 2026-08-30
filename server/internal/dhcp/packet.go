package dhcp

import (
	"encoding/hex"
	"net"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

// DHCPPacket wraps a single received DHCPv4 message with the receive-side
// context the rest of the engine needs (which interface it arrived on, the
// connection to reply on, and the peer address server4 resolved it to).
type DHCPPacket struct {
	ReceivedAt time.Time
	Interface  string
	Conn       net.PacketConn
	Peer       net.Addr
	Packet     *dhcpv4.DHCPv4

	// Dst is the destination IP address the received UDP datagram was
	// actually addressed to (captured off the socket via IP_PKTINFO -- see
	// listener.go), as opposed to anything derived from the DHCP payload
	// itself. handleRequestRenewRebind uses it to tell a client unicasting
	// straight to this server (RENEWING) apart from one broadcasting to
	// the whole segment (REBINDING), which the client-set BROADCAST header
	// flag cannot do. nil if it could not be captured.
	Dst net.IP
}

// ClientIDBytes returns the raw Option 61 (Client Identifier) value, or nil
// if the client did not send one.
func (p *DHCPPacket) ClientIDBytes() []byte {
	return p.Packet.Options.Get(dhcpv4.OptionClientIdentifier)
}

// ClientIDHex returns the hex-encoded Option 61 value (the canonical string
// form the engine passes to store.LeaseStore.AllocateLease's clientID
// parameter and uses to build "client_id:<hex>" reservation binding keys),
// or "" if absent.
func (p *DHCPPacket) ClientIDHex() string {
	if b := p.ClientIDBytes(); len(b) > 0 {
		return hex.EncodeToString(b)
	}
	return ""
}

// HostnameString returns the Option 12 (Host Name) value, lowercased and
// trimmed, or "" if absent. The canonical string form is lowercased for
// building "hostname:<value>" reservation binding keys.
func (p *DHCPPacket) HostnameString() string {
	hostname := dhcpv4.GetString(dhcpv4.GenericOptionCode(12), p.Packet.Options)
	return strings.TrimSpace(strings.ToLower(hostname))
}

// VendorClassBytes returns the raw Option 60 (Vendor Class Identifier)
// value, or nil if the client did not send one.
func (p *DHCPPacket) VendorClassBytes() []byte {
	return p.Packet.Options.Get(dhcpv4.GenericOptionCode(60))
}

// VendorClassHex returns the hex-encoded Option 60 value (the canonical
// string form used to build "vendor_class:<hex>" reservation binding keys),
// or "" if absent.
func (p *DHCPPacket) VendorClassHex() string {
	if b := p.VendorClassBytes(); len(b) > 0 {
		return hex.EncodeToString(b)
	}
	return ""
}

// UserClassBytes returns the raw Option 77 (User Class) value, or nil if
// the client did not send one.
func (p *DHCPPacket) UserClassBytes() []byte {
	return p.Packet.Options.Get(dhcpv4.GenericOptionCode(77))
}

// UserClassHex returns the hex-encoded Option 77 value (the canonical string
// form used to build "user_class:<hex>" reservation binding keys), or "" if
// absent.
func (p *DHCPPacket) UserClassHex() string {
	if b := p.UserClassBytes(); len(b) > 0 {
		return hex.EncodeToString(b)
	}
	return ""
}

// hwAddrString normalizes a hardware address to its canonical lowercase
// colon-separated string form (net.HardwareAddr.String()'s format), which is
// what the store's binding_key/mac_address values are built from.
func hwAddrString(mac net.HardwareAddr) string {
	if len(mac) == 0 {
		return ""
	}
	return mac.String()
}

// ipString safely stringifies a possibly-nil/unset IP for logging.
func ipString(ip net.IP) string {
	if ip == nil || isZeroIP(ip) {
		return ""
	}
	return ip.String()
}
