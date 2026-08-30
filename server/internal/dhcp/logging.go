package dhcp

import (
	"errors"
	"time"
)

// DHCPTransaction is one structured log line per DHCP transaction (request
// received -> response sent, NAK'd, or intentionally silent), matching the
// field set the task and docs/design/dhcp-engine.md specify so the UI's
// error/audit views can filter and render them.
type DHCPTransaction struct {
	Timestamp     time.Time
	XID           string
	ClientMAC     string
	ClientID      string
	ClientHostname string
	Interface     string
	MsgType       string
	ScopeID       string
	PoolID        string
	VRFID         string
	AllocatedIP   string
	LeaseDuration int
	RelayAgent    string // giaddr, "" if not relayed
	CircuitID     string // Option 82 sub-option 1, hex-encoded
	RemoteID      string // Option 82 sub-option 2, hex-encoded
	VendorClass   string // Option 60, hex-encoded
	UserClass     string // Option 77, hex-encoded
	ServerID      string
	Result        string // "OK", "NAK", "ERROR"
	Reason        string
	DurationMS    int64
}

// logTransaction emits one structured slog line per completed transaction.
func (e *Engine) logTransaction(t *DHCPTransaction) {
	e.log.Info("dhcp_transaction",
		"timestamp", t.Timestamp,
		"transaction_id", t.XID,
		"client_mac", t.ClientMAC,
		"client_id", t.ClientID,
		"client_hostname", t.ClientHostname,
		"interface", t.Interface,
		"msg_type", t.MsgType,
		"scope_id", t.ScopeID,
		"pool_id", t.PoolID,
		"vrf_id", t.VRFID,
		"allocated_ip", t.AllocatedIP,
		"lease_duration", t.LeaseDuration,
		"relay_agent", t.RelayAgent,
		"circuit_id", t.CircuitID,
		"remote_id", t.RemoteID,
		"vendor_class", t.VendorClass,
		"user_class", t.UserClass,
		"server_id", t.ServerID,
		"result", t.Result,
		"reason", t.Reason,
		"duration_ms", t.DurationMS,
	)
}

// logRequestError logs a transaction that failed before a reply could be
// composed or sent (dispatch's error path). Successful outcomes, including
// NAKs, log their own transaction via logTransaction from within dora.go so
// they can carry the scope/pool context available at that point; this path
// is for failures where that context may not have been resolved yet.
func (e *Engine) logRequestError(pkt *DHCPPacket, err error, dur time.Duration) {
	req := pkt.Packet
	reason := ""
	var reqErr *RequestError
	if errors.As(err, &reqErr) {
		reason = reqErr.Reason
	}

	relay, circuitID, remoteID := "", "", ""
	if ri := parseRelayInfo(req); ri != nil {
		circuitID, remoteID = ri.logFields()
	}
	if !isZeroIP(req.GatewayIPAddr) {
		relay = req.GatewayIPAddr.String()
	}

	e.log.Error("dhcp_transaction",
		"timestamp", pkt.ReceivedAt,
		"transaction_id", req.TransactionID.String(),
		"client_mac", hwAddrString(req.ClientHWAddr),
		"client_id", pkt.ClientIDHex(),
		"client_hostname", pkt.HostnameString(),
		"interface", pkt.Interface,
		"msg_type", req.MessageType().String(),
		"relay_agent", relay,
		"circuit_id", circuitID,
		"remote_id", remoteID,
		"vendor_class", pkt.VendorClassHex(),
		"user_class", pkt.UserClassHex(),
		"server_id", e.serverID,
		"result", "ERROR",
		"reason", reason,
		"error", err.Error(),
		"duration_ms", dur.Milliseconds(),
	)
}
