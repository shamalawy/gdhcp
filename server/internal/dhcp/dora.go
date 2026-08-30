package dhcp

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// handleDiscover implements DISCOVER -> OFFER: select a serving scope,
// allocate a candidate lease (store.LeaseStore.AllocateLease, driven via
// allocation.go), and reply with an OFFER carrying the allocated IP and
// resolved options.
func (e *Engine) handleDiscover(ctx context.Context, pkt *DHCPPacket) error {
	req := pkt.Packet

	sc, err := e.selectServingContext(ctx, pkt)
	if err != nil {
		return &RequestError{Err: err, MsgType: dhcpv4.MessageTypeDiscover, Reason: "scope_not_found"}
	}
	primaryScopeID := sc.Scopes[0].ID
	e.recordDiscover(primaryScopeID)

	result, err := e.allocate(ctx, sc.Scopes, pkt)
	if err != nil {
		e.recordNoLeases(primaryScopeID)
		return &RequestError{Err: err, MsgType: dhcpv4.MessageTypeDiscover, Reason: "no_leases"}
	}

	leaseSeconds := e.resolveLeaseSeconds(ctx, result.Scope.ID, result.Pool.ID, "")
	t1, t2 := e.computeTimers(ctx, leaseSeconds)
	serverID := e.serverIdentifierFor(pkt.Interface)

	resp, err := dhcpv4.NewReplyFromRequest(req,
		dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer),
		dhcpv4.WithYourIP(result.Lease.IPAddress),
		dhcpv4.WithServerIP(serverID),
	)
	if err != nil {
		return fmt.Errorf("dhcp: build offer: %w", err)
	}

	opts, err := e.buildReplyOptions(ctx, req, result.Scope, result.Pool, "", leaseSeconds, t1, t2, serverID, true)
	if err != nil {
		return fmt.Errorf("dhcp: resolve offer options: %w", err)
	}
	for _, o := range opts {
		resp.UpdateOption(o)
	}

	if err := e.sendReply(pkt, resp); err != nil {
		return fmt.Errorf("dhcp: send offer: %w", err)
	}

	e.recordOffer(result.Scope.ID)
	e.logTransaction(e.transactionLog(pkt, sc, "DISCOVER", result.Scope.ID, result.Pool.ID,
		result.Lease.IPAddress, int(leaseSeconds), "OK", ""))
	return nil
}

// handleRequest classifies a REQUEST into SELECTING, INIT-REBOOT, or
// RENEWING/REBINDING per RFC 2131 §4.3.2 and dispatches accordingly.
func (e *Engine) handleRequest(ctx context.Context, pkt *DHCPPacket) error {
	req := pkt.Packet

	reqIP := req.RequestedIPAddress()
	hasRequestedIP := reqIP != nil && !isZeroIP(reqIP)

	switch {
	case !isZeroIP(req.ClientIPAddr):
		return e.handleRequestRenewRebind(ctx, pkt)
	case hasRequestedIP && req.ServerIdentifier() != nil:
		return e.handleRequestSelecting(ctx, pkt)
	case hasRequestedIP:
		return e.handleRequestInitReboot(ctx, pkt)
	default:
		return &RequestError{Err: ErrInvalidRequest, MsgType: dhcpv4.MessageTypeRequest, Reason: "missing_ciaddr_and_requested_ip"}
	}
}

// handleRequestSelecting handles a REQUEST following an OFFER the client
// accepted (ciaddr=0, requested-ip and server-id both set). A server-id that
// doesn't match this node means the client selected a different server's
// offer, so this node stays silent (RFC 2131 §4.3.2).
func (e *Engine) handleRequestSelecting(ctx context.Context, pkt *DHCPPacket) error {
	req := pkt.Packet
	wantServerID := e.serverIdentifierFor(pkt.Interface)
	if sid := req.ServerIdentifier(); sid != nil && !sid.Equal(wantServerID) {
		return nil
	}

	reqIP := req.RequestedIPAddress()
	sc, err := e.selectServingContext(ctx, pkt)
	if err != nil {
		return &RequestError{Err: err, MsgType: dhcpv4.MessageTypeRequest, Reason: "scope_not_found"}
	}
	primaryScopeID := sc.Scopes[0].ID
	e.recordRequest(primaryScopeID)

	lease, pool, scope, err := e.confirmOrReAllocate(ctx, sc.Scopes, pkt, reqIP)
	if err != nil {
		e.recordNak(primaryScopeID)
		return e.sendNak(pkt, wantServerID, sc, "requested_ip_unavailable", err)
	}
	return e.sendAck(ctx, pkt, sc, lease, pool, scope, wantServerID, "REQUEST-SELECTING")
}

// handleRequestInitReboot handles a client restarting with a known lease
// (ciaddr=0, requested-ip set, no server-id — broadcast to all servers).
// Per the design doc: if the requested IP isn't in any scope this server
// knows about, NAK; otherwise confirm or NAK.
func (e *Engine) handleRequestInitReboot(ctx context.Context, pkt *DHCPPacket) error {
	req := pkt.Packet
	wantServerID := e.serverIdentifierFor(pkt.Interface)
	reqIP := req.RequestedIPAddress()

	sc, err := e.selectServingContext(ctx, pkt)
	if err != nil {
		return e.sendNak(pkt, wantServerID, nil, "unknown_scope", err)
	}
	primaryScopeID := sc.Scopes[0].ID
	e.recordRequest(primaryScopeID)

	// Validate reqIP against a reqIP-independent scope set, never sc.Scopes
	// directly: for a direct (unrelayed) request, directScopes can fall
	// back to selecting scopes by matching this exact reqIP (its tier 2),
	// which would make the check below tautological — always pass,
	// regardless of whether reqIP is actually on a network this interface
	// serves. selectByGiaddr's result (a relayed request) has no such
	// fallback — it's chosen by giaddr alone — so it's already safe as-is.
	wrongNetworkCandidates := sc.Scopes
	if !sc.Relayed {
		ns, err := e.networkScopes(ctx, pkt.Interface, sc.VRF)
		if err != nil {
			e.recordNak(primaryScopeID)
			return e.sendNak(pkt, wantServerID, sc, "unknown_scope", err)
		}
		wrongNetworkCandidates = ns
	}

	inScope := false
	for _, s := range wrongNetworkCandidates {
		if s.Network == nil {
			continue
		}
		n := net.IPNet(*s.Network)
		if n.Contains(reqIP) {
			inScope = true
			break
		}
	}
	if !inScope {
		e.recordNak(primaryScopeID)
		return e.sendNak(pkt, wantServerID, sc, "requested_ip_not_in_scope", ErrIPNotInScope)
	}

	lease, pool, scope, err := e.confirmOrReAllocate(ctx, sc.Scopes, pkt, reqIP)
	if err != nil {
		e.recordNak(primaryScopeID)
		return e.sendNak(pkt, wantServerID, sc, "requested_ip_unavailable", err)
	}
	return e.sendAck(ctx, pkt, sc, lease, pool, scope, wantServerID, "REQUEST-INIT-REBOOT")
}

// handleRequestRenewRebind handles RENEWING (unicast to this server,
// ciaddr set) and REBINDING (broadcast, ciaddr set) per RFC 2131 §4.3.2. A
// server-id present and not matching this node means the RENEW targeted a
// different server. When this server has no record of the lease: RENEWING
// gets a NAK (the client is unicasting specifically to us); REBINDING stays
// silent so another server in the cluster/network can answer instead. See
// the isRenewing comment below for how the two are told apart.
func (e *Engine) handleRequestRenewRebind(ctx context.Context, pkt *DHCPPacket) error {
	req := pkt.Packet
	wantServerID := e.serverIdentifierFor(pkt.Interface)
	// isRenewing distinguishes RENEWING from REBINDING using the received
	// datagram's actual destination address (pkt.Dst, captured off the
	// socket via IP_PKTINFO — see listener.go), not the DHCP header
	// BROADCAST flag (what IsUnicast() reads): that flag only says whether
	// the *client* can receive unicast pre-configuration, not whether this
	// packet itself arrived unicast-to-us — a compliant RENEWING and
	// REBINDING client both normally leave it unset (each already has a
	// working ciaddr), so IsUnicast() cannot reliably tell the two apart on
	// its own. A nonzero giaddr is a separate, unambiguous signal: a
	// RENEWING client unicasts directly to its prior server and is never
	// relayed, so any relayed packet here is necessarily REBINDING and must
	// never be NAK'd on a not-found lease (RFC 2131 §4.3.2). For a directly
	// connected (unrelayed) client, pkt.Dst does the same job the BROADCAST
	// flag couldn't: it's RENEWING only when the datagram was actually
	// addressed to this server rather than broadcast to the segment. When
	// the destination couldn't be captured for some reason, default to
	// REBINDING's stay-silent behavior rather than risk a wrongful
	// segment-wide NAK.
	relayed := !isZeroIP(req.GatewayIPAddr)
	isRenewing := !relayed && pkt.Dst != nil && !isZeroIP(pkt.Dst) && !pkt.Dst.Equal(net.IPv4bcast)

	if sid := req.ServerIdentifier(); sid != nil && !sid.Equal(wantServerID) {
		return nil
	}

	sc, err := e.selectServingContext(ctx, pkt)
	if err != nil {
		if isRenewing {
			return e.sendNak(pkt, wantServerID, nil, "scope_not_found", err)
		}
		return nil
	}
	primaryScopeID := sc.Scopes[0].ID
	e.recordRequest(primaryScopeID)

	lease, pool, scope, err := e.renewExisting(ctx, sc.Scopes, req.ClientHWAddr, req.ClientIPAddr, optionalHostname(pkt))
	if err != nil {
		e.recordNak(primaryScopeID)
		if isRenewing {
			return e.sendNak(pkt, wantServerID, sc, "lease_not_found", err)
		}
		return nil
	}
	return e.sendAck(ctx, pkt, sc, lease, pool, scope, wantServerID, "REQUEST-RENEW")
}

// confirmOrReAllocate re-runs allocation (which returns the client's
// existing lease first, via A6's renew-affinity inside store.AllocateLease)
// and verifies the result matches what the client asked for; a mismatch
// (the offer expired and the IP went to someone else) is reported as
// ErrIPConflict so the caller NAKs.
func (e *Engine) confirmOrReAllocate(ctx context.Context, scopes []*models.DHCPScope, pkt *DHCPPacket, requestedIP net.IP) (*models.DHCPLease, *models.DHCPPool, *models.DHCPScope, error) {
	result, err := e.allocate(ctx, scopes, pkt)
	if err != nil {
		return nil, nil, nil, err
	}
	if requestedIP != nil && !isZeroIP(requestedIP) && !result.Lease.IPAddress.Equal(requestedIP) {
		return nil, nil, nil, fmt.Errorf("%w: client requested %s, allocator returned %s", ErrIPConflict, requestedIP, result.Lease.IPAddress)
	}
	return result.Lease, result.Pool, result.Scope, nil
}

// renewExisting looks up the lease bound to ciaddr across the candidate
// scopes' pools and extends it, verifying the requesting MAC matches the
// lease on file (store.LeaseStore.RenewLease also enforces this, but
// checking here lets us return models.ErrLeaseNotFound instead of a raw
// store rejection when it's a MAC mismatch).
func (e *Engine) renewExisting(ctx context.Context, scopes []*models.DHCPScope, mac net.HardwareAddr, ciaddr net.IP, hostname *string) (*models.DHCPLease, *models.DHCPPool, *models.DHCPScope, error) {
	lease, pool, scope, err := e.findLeaseByIPAcrossScopes(ctx, scopes, ciaddr)
	if err != nil {
		return nil, nil, nil, err
	}
	macStr := hwAddrString(mac)
	if lease.ClientMAC == nil || !strings.EqualFold(net.HardwareAddr(*lease.ClientMAC).String(), macStr) {
		return nil, nil, nil, models.ErrLeaseNotFound
	}

	leaseSeconds := e.resolveLeaseSeconds(ctx, scope.ID, pool.ID, "")
	newExpiry := time.Now().Add(time.Duration(leaseSeconds) * time.Second)
	renewed, err := e.store.RenewLease(ctx, lease.ID, macStr, newExpiry, hostname)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dhcp: renew lease %s: %w", lease.ID, err)
	}
	return renewed, pool, scope, nil
}

// findLeaseByIPAcrossScopes searches every pool of every candidate scope
// for an active lease bound to ip. Used by renew/decline/release, which all
// need to locate a lease by IP without already knowing its pool.
func (e *Engine) findLeaseByIPAcrossScopes(ctx context.Context, scopes []*models.DHCPScope, ip net.IP) (*models.DHCPLease, *models.DHCPPool, *models.DHCPScope, error) {
	for _, scope := range scopes {
		pools, err := e.store.GetPoolsByScope(ctx, scope.ID)
		if err != nil {
			continue
		}
		for _, pool := range pools {
			lease, err := e.store.GetLeaseByIP(ctx, pool.ID, ip)
			if err != nil || lease == nil {
				continue
			}
			return lease, pool, scope, nil
		}
	}
	return nil, nil, nil, models.ErrLeaseNotFound
}

// handleDecline implements DECLINE: the client reports the offered/leased
// address as unusable (e.g. an ARP conflict it detected). No response is
// sent per RFC 2131. store.LeaseStore has no method that transitions a
// lease straight to models.LeaseStateAbandoned (only Release, which frees
// the address for immediate reallocation) — see notes_for_integration; the
// best available action today is to release the binding so this client is
// not handed the same conflicted address again.
func (e *Engine) handleDecline(ctx context.Context, pkt *DHCPPacket) error {
	req := pkt.Packet
	declinedIP := req.RequestedIPAddress()
	if isZeroIP(declinedIP) {
		declinedIP = req.ClientIPAddr
	}
	macStr := hwAddrString(req.ClientHWAddr)

	sc, scErr := e.selectServingContext(ctx, pkt)
	scopeID := ""
	var outcome error
	if scErr == nil {
		lease, _, scope, ferr := e.findLeaseByIPAcrossScopes(ctx, sc.Scopes, declinedIP)
		if ferr == nil {
			scopeID = scope.ID
			outcome = e.store.ReleaseLease(ctx, lease.ID, macStr)
		} else {
			outcome = ferr
		}
	} else {
		outcome = scErr
	}

	e.recordDecline(scopeID)
	result, reason := "OK", ""
	if outcome != nil {
		result, reason = "ERROR", outcome.Error()
	}
	e.logTransaction(&DHCPTransaction{
		Timestamp: pkt.ReceivedAt, XID: req.TransactionID.String(), ClientMAC: macStr,
		Interface: pkt.Interface, MsgType: "DECLINE", ScopeID: scopeID,
		AllocatedIP: ipString(declinedIP), Result: result, Reason: reason,
		ServerID: e.serverID, DurationMS: time.Since(pkt.ReceivedAt).Milliseconds(),
	})
	return nil
}

// handleRelease implements RELEASE: the client gives up its lease early. No
// response is sent per RFC 2131.
func (e *Engine) handleRelease(ctx context.Context, pkt *DHCPPacket) error {
	req := pkt.Packet
	ip := req.ClientIPAddr
	macStr := hwAddrString(req.ClientHWAddr)

	sc, scErr := e.selectServingContext(ctx, pkt)
	scopeID := ""
	var outcome error
	if scErr == nil {
		lease, _, scope, ferr := e.findLeaseByIPAcrossScopes(ctx, sc.Scopes, ip)
		if ferr == nil {
			scopeID = scope.ID
			outcome = e.store.ReleaseLease(ctx, lease.ID, macStr)
		} else {
			outcome = ferr
		}
	} else {
		outcome = scErr
	}

	e.recordRelease(scopeID)
	result, reason := "OK", ""
	if outcome != nil {
		result, reason = "ERROR", outcome.Error()
	}
	e.logTransaction(&DHCPTransaction{
		Timestamp: pkt.ReceivedAt, XID: req.TransactionID.String(), ClientMAC: macStr,
		Interface: pkt.Interface, MsgType: "RELEASE", ScopeID: scopeID,
		AllocatedIP: ipString(ip), Result: result, Reason: reason,
		ServerID: e.serverID, DurationMS: time.Since(pkt.ReceivedAt).Milliseconds(),
	})
	return nil
}

// handleInform implements INFORM: the client already has an address
// (statically configured) and wants configuration options only. No lease is
// allocated or recorded (RFC 2131 §3.4).
func (e *Engine) handleInform(ctx context.Context, pkt *DHCPPacket) error {
	req := pkt.Packet

	sc, err := e.selectServingContext(ctx, pkt)
	if err != nil {
		return &RequestError{Err: err, MsgType: dhcpv4.MessageTypeInform, Reason: "scope_not_found"}
	}
	scope := sc.Scopes[0]

	var pool *models.DHCPPool
	if !isZeroIP(req.ClientIPAddr) {
		if pools, perr := e.store.GetPoolsByScope(ctx, scope.ID); perr == nil {
			for _, p := range pools {
				if poolContainsIP(p, req.ClientIPAddr) {
					pool = p
					break
				}
			}
		}
	}

	serverID := e.serverIdentifierFor(pkt.Interface)
	resp, err := dhcpv4.NewReplyFromRequest(req,
		dhcpv4.WithMessageType(dhcpv4.MessageTypeAck),
		dhcpv4.WithServerIP(serverID),
		dhcpv4.WithClientIP(req.ClientIPAddr),
	)
	if err != nil {
		return fmt.Errorf("dhcp: build inform ack: %w", err)
	}

	poolID := ""
	if pool != nil {
		poolID = pool.ID
	}
	opts, err := e.buildReplyOptions(ctx, req, scope, pool, "", 0, 0, 0, serverID, false)
	if err != nil {
		return fmt.Errorf("dhcp: resolve inform options: %w", err)
	}
	for _, o := range opts {
		resp.UpdateOption(o)
	}

	if err := e.sendReply(pkt, resp); err != nil {
		return fmt.Errorf("dhcp: send inform ack: %w", err)
	}

	e.logTransaction(e.transactionLog(pkt, sc, "INFORM", scope.ID, poolID, nil, 0, "OK", ""))
	return nil
}

// sendAck builds and sends an ACK for a confirmed/renewed lease.
func (e *Engine) sendAck(ctx context.Context, pkt *DHCPPacket, sc *servingContext, lease *models.DHCPLease, pool *models.DHCPPool, scope *models.DHCPScope, serverID net.IP, phase string) error {
	req := pkt.Packet

	leaseSeconds := uint32(time.Until(lease.LeaseEnd).Seconds())
	if leaseSeconds <= 0 {
		leaseSeconds = e.resolveLeaseSeconds(ctx, scope.ID, pool.ID, "")
	}
	t1, t2 := e.computeTimers(ctx, leaseSeconds)

	resp, err := dhcpv4.NewReplyFromRequest(req,
		dhcpv4.WithMessageType(dhcpv4.MessageTypeAck),
		dhcpv4.WithYourIP(lease.IPAddress),
		dhcpv4.WithServerIP(serverID),
		dhcpv4.WithClientIP(req.ClientIPAddr),
	)
	if err != nil {
		return fmt.Errorf("dhcp: build ack: %w", err)
	}

	opts, err := e.buildReplyOptions(ctx, req, scope, pool, "", leaseSeconds, t1, t2, serverID, true)
	if err != nil {
		return fmt.Errorf("dhcp: resolve ack options: %w", err)
	}
	for _, o := range opts {
		resp.UpdateOption(o)
	}

	if err := e.sendReply(pkt, resp); err != nil {
		return fmt.Errorf("dhcp: send ack: %w", err)
	}

	e.recordAck(scope.ID)
	e.logTransaction(e.transactionLog(pkt, sc, phase, scope.ID, pool.ID, lease.IPAddress, int(leaseSeconds), "OK", ""))
	return nil
}

// sendNak builds and sends a NAK. Per RFC 2131 §4.3.2 a NAK is always
// broadcast unless relayed (never unicast to ciaddr — the binding being
// denied is exactly why ciaddr can't be trusted), handled by sendNakReply.
func (e *Engine) sendNak(pkt *DHCPPacket, serverID net.IP, sc *servingContext, reason string, cause error) error {
	req := pkt.Packet

	resp, err := dhcpv4.NewReplyFromRequest(req,
		dhcpv4.WithMessageType(dhcpv4.MessageTypeNak),
		dhcpv4.WithServerIP(serverID),
	)
	if err != nil {
		return fmt.Errorf("dhcp: build nak: %w", err)
	}
	resp.ClientIPAddr = net.IPv4zero
	resp.UpdateOption(dhcpv4.OptServerIdentifier(serverID))
	if cause != nil {
		resp.UpdateOption(dhcpv4.OptMessage(cause.Error()))
	}

	if err := e.sendNakReply(pkt, resp); err != nil {
		return fmt.Errorf("dhcp: send nak: %w", err)
	}

	e.logTransaction(e.transactionLog(pkt, sc, "NAK", "", "", nil, 0, "NAK", reason))
	return nil
}

// transactionLog assembles a DHCPTransaction from the pieces every DORA
// handler above has on hand, filling relay/circuit/remote-id fields from
// the request and VRF ID from the resolved serving context (sc may be nil
// for a NAK sent before scope resolution succeeded).
func (e *Engine) transactionLog(pkt *DHCPPacket, sc *servingContext, msgType, scopeID, poolID string, allocatedIP net.IP, leaseDuration int, result, reason string) *DHCPTransaction {
	req := pkt.Packet
	vrfID := ""
	if sc != nil && sc.VRF != nil {
		vrfID = sc.VRF.ID
	}
	relay := ""
	if !isZeroIP(req.GatewayIPAddr) {
		relay = req.GatewayIPAddr.String()
	}
	circuitID, remoteID := parseRelayInfo(req).logFields()

	return &DHCPTransaction{
		Timestamp:      pkt.ReceivedAt,
		XID:            req.TransactionID.String(),
		ClientMAC:      hwAddrString(req.ClientHWAddr),
		ClientID:       pkt.ClientIDHex(),
		ClientHostname: pkt.HostnameString(),
		Interface:      pkt.Interface,
		MsgType:        msgType,
		ScopeID:        scopeID,
		PoolID:         poolID,
		VRFID:          vrfID,
		AllocatedIP:    ipString(allocatedIP),
		LeaseDuration:  leaseDuration,
		RelayAgent:     relay,
		CircuitID:      circuitID,
		RemoteID:       remoteID,
		VendorClass:    pkt.VendorClassHex(),
		UserClass:      pkt.UserClassHex(),
		ServerID:       e.serverID,
		Result:         result,
		Reason:         reason,
		DurationMS:     time.Since(pkt.ReceivedAt).Milliseconds(),
	}
}

// sendReply computes the RFC 2131 §4.1 destination address for a
// successful (non-NAK) reply and writes it to the wire:
//  1. giaddr set (relayed): unicast to the relay, port 67.
//  2. ciaddr set (renewing/informing): unicast to the client's own address,
//     port 68.
//  3. Client's broadcast flag set: broadcast, port 68.
//  4. Otherwise: unicast the offered/assigned address (yiaddr), port 68 —
//     the client isn't listening on ciaddr yet, but is expected to accept
//     traffic addressed to its about-to-be-configured address once ARP
//     resolves it (standard behavior for servers without raw-socket access
//     to address the client by hardware address directly).
func (e *Engine) sendReply(pkt *DHCPPacket, resp *dhcpv4.DHCPv4) error {
	dst := replyDestination(pkt.Packet, resp, pkt.Peer)
	_, err := pkt.Conn.WriteTo(resp.ToBytes(), dst)
	return err
}

func replyDestination(req, resp *dhcpv4.DHCPv4, peer net.Addr) *net.UDPAddr {
	if !isZeroIP(req.GatewayIPAddr) {
		return &net.UDPAddr{IP: req.GatewayIPAddr, Port: dhcpv4.ServerPort}
	}
	if !isZeroIP(req.ClientIPAddr) {
		return &net.UDPAddr{IP: req.ClientIPAddr, Port: dhcpv4.ClientPort}
	}
	if req.IsBroadcast() {
		return &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	}
	if !isZeroIP(resp.YourIPAddr) {
		return &net.UDPAddr{IP: resp.YourIPAddr, Port: dhcpv4.ClientPort}
	}
	if up, ok := peer.(*net.UDPAddr); ok && up.IP != nil {
		return &net.UDPAddr{IP: up.IP, Port: dhcpv4.ClientPort}
	}
	return &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
}

// sendNakReply is sendReply's NAK-specific counterpart: broadcast unless
// relayed, never unicast to ciaddr (see sendNak's doc comment).
func (e *Engine) sendNakReply(pkt *DHCPPacket, resp *dhcpv4.DHCPv4) error {
	req := pkt.Packet
	var dst *net.UDPAddr
	if !isZeroIP(req.GatewayIPAddr) {
		dst = &net.UDPAddr{IP: req.GatewayIPAddr, Port: dhcpv4.ServerPort}
	} else {
		dst = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	}
	_, err := pkt.Conn.WriteTo(resp.ToBytes(), dst)
	return err
}

// poolContainsIP reports whether ip falls within pool's [RangeStart,
// RangeEnd] (inclusive), comparing IPv4 4-byte forms.
func poolContainsIP(pool *models.DHCPPool, ip net.IP) bool {
	v4 := ip.To4()
	start := pool.RangeStart.To4()
	end := pool.RangeEnd.To4()
	if v4 == nil || start == nil || end == nil {
		return false
	}
	return bytes.Compare(start, v4) <= 0 && bytes.Compare(v4, end) <= 0
}
