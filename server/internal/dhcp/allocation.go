package dhcp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// allocResult bundles a claimed lease with the pool/scope it came from, so
// callers (dora.go) have everything needed for option resolution and
// logging without a second round-trip.
type allocResult struct {
	Lease *models.DHCPLease
	Pool  *models.DHCPPool
	Scope *models.DHCPScope
}

// buildClientIdentity constructs a models.ClientIdentity from a received
// DHCPv4 packet and its parsed relay agent information. Each field is
// canonicalised to the exact form models.BindingKeys() expects: lowercase
// colon-separated MAC, lowercase hex for opaque values, lowercased hostname.
// The supplied relayInfo may be nil (no Option 82 present).
func buildClientIdentity(pkt *DHCPPacket, relay *relayInfo) models.ClientIdentity {
	ident := models.ClientIdentity{
		MAC:         hwAddrString(pkt.Packet.ClientHWAddr),
		ClientID:    pkt.ClientIDHex(),
		Hostname:    pkt.HostnameString(),
		VendorClass: pkt.VendorClassHex(),
		UserClass:   pkt.UserClassHex(),
	}
	if relay != nil {
		if len(relay.CircuitID) > 0 {
			ident.CircuitID = hex.EncodeToString(relay.CircuitID)
		}
		if len(relay.RemoteID) > 0 {
			ident.RemoteID = hex.EncodeToString(relay.RemoteID)
		}
	}
	return ident
}

// optionalHostname returns the client's option 12 name for persisting on a
// lease, or nil when the client sent none -- in which case the stored value is
// left alone rather than being blanked.
func optionalHostname(pkt *DHCPPacket) *string {
	h := pkt.HostnameString()
	if h == "" {
		return nil
	}
	return &h
}

// allocate drives store.LeaseStore per amendment A6: the store's
// AllocateLease already implements reservation-match-then-renew-affinity-
// then-candidate-scan-with-retry internally, keyed by the client's
// identifiers in models.ClientIdentity. This function's job is the part A6
// leaves to the caller: try pools in priority order (store.GetPoolsByScope
// already returns them so ordered) across every candidate scope (shared-
// network support), skipping disabled pools/scopes and pools the store
// reports exhausted (models.ErrNoLeases), until one succeeds or every pool
// has been tried.
func (e *Engine) allocate(ctx context.Context, scopes []*models.DHCPScope, pkt *DHCPPacket) (*allocResult, error) {
	relay := parseRelayInfo(pkt.Packet)
	ident := buildClientIdentity(pkt, relay)
	hostname := (*string)(nil)
	if ident.Hostname != "" {
		hostname = &ident.Hostname
	}

	var lastErr error = models.ErrNoLeases
	for _, scope := range scopes {
		if !scope.IsEnabled {
			continue
		}
		pools, err := e.store.GetPoolsByScope(ctx, scope.ID)
		if err != nil {
			lastErr = fmt.Errorf("dhcp: list pools for scope %s: %w", scope.ID, err)
			continue
		}
		for _, pool := range pools {
			if !pool.IsEnabled {
				continue
			}
			// Resolve the lease length against this scope/pool before
			// claiming, so the row written to the store carries the same
			// expiry the OFFER/ACK advertises rather than a store default.
			leaseDur := time.Duration(e.resolveLeaseSeconds(ctx, scope.ID, pool.ID, "")) * time.Second
			lease, err := e.allocateFromPool(ctx, pool, ident, hostname, leaseDur)
			if err != nil {
				if errors.Is(err, models.ErrNoLeases) {
					lastErr = err
					continue
				}
				return nil, err
			}
			return &allocResult{Lease: lease, Pool: pool, Scope: scope}, nil
		}
	}
	return nil, lastErr
}

// maxPingRetries bounds how many times allocateFromPool will ask the store
// for a fresh candidate after a ping-check finds the previous one already
// answering (i.e. in use despite having no lease record) — mirrors A6's own
// "retry ≤3 rounds" allocation-race budget.
const maxPingRetries = 3

// allocateFromPool calls store.AllocateLease for one pool and, if
// ping-check is enabled, verifies the claimed address doesn't already
// answer an ICMP echo before returning it — releasing and retrying against
// the same pool (a fresh candidate; A6's ON CONFLICT DO NOTHING scan skips
// what's already claimed) if it does.
func (e *Engine) allocateFromPool(ctx context.Context, pool *models.DHCPPool, ident models.ClientIdentity, hostname *string, leaseDur time.Duration) (*models.DHCPLease, error) {
	macStr := ident.MAC
	for attempt := 0; attempt < maxPingRetries; attempt++ {
		lease, err := e.store.AllocateLease(ctx, pool.ID, ident, hostname, leaseDur)
		if err != nil {
			return nil, err
		}
		if !e.pingCheckEnabled {
			return lease, nil
		}
		if !e.pingInUse(ctx, lease.IPAddress) {
			return lease, nil
		}
		e.log.Warn("dhcp: ping-check found allocated address already in use, abandoning and retrying",
			"pool_id", pool.ID, "ip", lease.IPAddress.String(), "attempt", attempt+1)
		// AbandonLease, not ReleaseLease: a released lease is immediately
		// reclaimable via AllocateLease's renew-affinity step, so the very
		// next AllocateLease call for this same client would just be handed
		// this exact IP straight back (mostRecentClientLease sees it as the
		// freshest row) — the retry would keep re-pinging the identical
		// in-use address instead of ever reaching a different candidate.
		if abErr := e.store.AbandonLease(ctx, lease.ID, macStr); abErr != nil {
			e.log.Warn("dhcp: failed to abandon ping-conflicted lease", "lease_id", lease.ID, "error", abErr)
		}
	}
	return nil, models.ErrNoLeases
}

// pingInUse sends an unprivileged (UDP-datagram) ICMP echo request to ip and
// reports whether a reply was received before pingCheckTimeout elapses. Per
// the design doc, this must degrade silently (return false, i.e. "assume
// free") when ICMP sockets aren't permitted in the runtime environment
// rather than block allocation.
func (e *Engine) pingInUse(_ context.Context, ip net.IP) bool {
	if ip == nil {
		return false
	}
	timeout := e.pingCheckTimeout
	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}

	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		e.log.Debug("dhcp: ping-check unavailable (no permission for unprivileged ICMP), skipping", "error", err)
		return false
	}
	defer conn.Close()

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xffff,
			Seq:  1,
			Data: []byte("gdhcp-ping-check"),
		},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return false
	}
	if _, err := conn.WriteTo(wb, &net.UDPAddr{IP: ip}); err != nil {
		return false
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return false
	}

	rb := make([]byte, 1500)
	n, _, err := conn.ReadFrom(rb)
	if err != nil {
		// Timeout or read error: no reply within the window, treat as free.
		return false
	}
	rm, err := icmp.ParseMessage(ipv4.ICMPTypeEcho.Protocol(), rb[:n])
	if err != nil {
		return false
	}
	return rm.Type == ipv4.ICMPTypeEchoReply
}
