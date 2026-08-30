package dhcp

import (
	"encoding/hex"
	"net"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

// relayInfo holds the RFC 3046 Option 82 sub-options a relay agent attached
// to a request, decoded for logging and (for ServerIDOverride) potential
// server-identity override handling.
type relayInfo struct {
	CircuitID        []byte
	RemoteID         []byte
	ServerIDOverride net.IP
}

// parseRelayInfo extracts Option 82 sub-options from req, or returns nil if
// the request carries no relay agent information.
func parseRelayInfo(req *dhcpv4.DHCPv4) *relayInfo {
	ra := req.RelayAgentInfo()
	if ra == nil {
		return nil
	}
	info := &relayInfo{}
	if v := ra.Options.Get(dhcpv4.AgentCircuitIDSubOption); len(v) > 0 {
		info.CircuitID = v
	}
	if v := ra.Options.Get(dhcpv4.AgentRemoteIDSubOption); len(v) > 0 {
		info.RemoteID = v
	}
	if v := ra.Options.Get(dhcpv4.ServerIdentifierOverrideSubOption); len(v) == net.IPv4len {
		info.ServerIDOverride = net.IP(v)
	}
	return info
}

// logFields renders the relay sub-options as hex strings for structured
// logging.LogTransaction (circuit ID / remote ID are opaque vendor-specific
// byte blobs, not guaranteed printable).
func (r *relayInfo) logFields() (circuitID, remoteID string) {
	if r == nil {
		return "", ""
	}
	if len(r.CircuitID) > 0 {
		circuitID = hex.EncodeToString(r.CircuitID)
	}
	if len(r.RemoteID) > 0 {
		remoteID = hex.EncodeToString(r.RemoteID)
	}
	return circuitID, remoteID
}

// Option 82 is echoed back verbatim in replies by dhcpv4.NewReplyFromRequest
// (it calls WithOptionCopied(request, dhcpv4.OptionRelayAgentInformation)),
// so dora.go's reply construction needs no extra step for that — this file
// only handles the read/log side.
