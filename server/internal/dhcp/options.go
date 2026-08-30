package dhcp

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/rfc1035label"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// alwaysSendCodes is the "always-send set" from the task spec: sent in
// every OFFER/ACK regardless of the client's Parameter Request List. Codes
// 51/54/58/59 (lease time, server id, T1, T2) are computed by the engine
// itself (lease.go/vrf.go), not resolved from the store; 1/3/6/15 (subnet
// mask, router, DNS, domain name) are resolved from the store like any
// other option, with 1 falling back to a value derived from the scope's
// network if the admin hasn't configured one explicitly.
var alwaysSendCodes = []uint8{1, 3, 6, 15, 51, 54, 58, 59}

// computedCodes are resolved by the engine directly (lease.go/vrf.go), not
// looked up in the option store, so the generic resolution loop skips them.
var computedCodes = map[uint8]bool{51: true, 54: true, 58: true, 59: true}

// buildReplyOptions resolves the effective option set for a reply per
// docs/design/dhcp-engine.md's four-step pipeline (fetch every inheritance
// level via store.ConfigStore.ResolveOption, which already implements the
// reservation > pool > scope > template > global precedence in one call;
// filter by PRL plus the always-send set; encode by catalog value_type).
// scope may be nil only for degenerate INFORM cases with no matched scope
// (should not normally happen, since selectServingContext always resolves
// one); pool/reservationID may be "" when not applicable to this message.
// leaseSeconds/t1/t2 are only included (as options 51/58/59) when
// includeLeaseTimers is true (false for INFORM, which establishes no
// binding per RFC 2131 §3.4).
func (e *Engine) buildReplyOptions(
	ctx context.Context,
	req *dhcpv4.DHCPv4,
	scope *models.DHCPScope,
	pool *models.DHCPPool,
	reservationID string,
	leaseSeconds, t1, t2 uint32,
	serverID net.IP,
	includeLeaseTimers bool,
) ([]dhcpv4.Option, error) {
	scopeID := ""
	if scope != nil {
		scopeID = scope.ID
	}
	poolID := ""
	if pool != nil {
		poolID = pool.ID
	}

	candidates, err := e.candidateOptionCodes(ctx, req, scopeID, poolID, reservationID)
	if err != nil {
		return nil, err
	}

	var opts []dhcpv4.Option
	haveMask := false
	for code := range candidates {
		if computedCodes[code] {
			continue
		}
		opt, err := e.store.ResolveOption(ctx, int16(code), scopeID, poolID, reservationID)
		if err != nil {
			e.log.Warn("dhcp: resolve option failed, skipping", "code", code, "error", err)
			continue
		}
		if opt == nil || !opt.IsEnabled {
			continue
		}
		cat, err := e.catalogEntryFor(ctx, opt)
		if err != nil || cat == nil {
			e.log.Warn("dhcp: option catalog entry unavailable, skipping", "code", code, "error", err)
			continue
		}
		encoded, err := encodeOptionValue(cat.ValueType, opt.Value)
		if err != nil {
			e.log.Warn("dhcp: encode option value failed, skipping", "code", code, "value_type", cat.ValueType, "error", err)
			continue
		}
		opts = append(opts, dhcpv4.OptGeneric(dhcpv4.GenericOptionCode(code), encoded))
		if code == 1 {
			haveMask = true
		}
	}

	if !haveMask && scope != nil && scope.Network != nil {
		n := net.IPNet(*scope.Network)
		if len(n.Mask) > 0 {
			opts = append(opts, dhcpv4.OptSubnetMask(n.Mask))
		}
	}

	if serverID != nil {
		opts = append(opts, dhcpv4.OptServerIdentifier(serverID))
	}
	if includeLeaseTimers {
		opts = append(opts,
			dhcpv4.OptIPAddressLeaseTime(time.Duration(leaseSeconds)*time.Second),
			dhcpv4.OptRenewTimeValue(time.Duration(t1)*time.Second),
			dhcpv4.OptRebindingTimeValue(time.Duration(t2)*time.Second),
		)
	}

	return opts, nil
}

// candidateOptionCodes builds the set of option codes worth resolving: the
// always-send set, plus the client's PRL (option 55) if it sent one. Per
// docs/CONTRACTS.md Part 3 ("Lazy-load only options in client's Parameter
// Request List") that PRL-driven set is the fast path; if the client sent
// no PRL at all, RFC 2131 §3.5 says to fall back to sending everything
// available, so every code configured anywhere in the inheritance chain
// (global/template/scope/pool/reservation) is enumerated instead.
func (e *Engine) candidateOptionCodes(ctx context.Context, req *dhcpv4.DHCPv4, scopeID, poolID, reservationID string) (map[uint8]bool, error) {
	candidates := map[uint8]bool{}
	for _, c := range alwaysSendCodes {
		candidates[c] = true
	}

	prl := req.ParameterRequestList()
	for _, c := range prl {
		candidates[c.Code()] = true
	}

	if len(prl) == 0 {
		all, err := e.allConfiguredCodes(ctx, scopeID, poolID, reservationID)
		if err != nil {
			return nil, err
		}
		for c := range all {
			candidates[c] = true
		}
	}

	return candidates, nil
}

// allConfiguredCodes unions the option codes configured at every level of
// the inheritance chain reachable from scopeID/poolID/reservationID: global,
// every template applied to the scope, the scope itself, the pool, and the
// reservation (if any).
func (e *Engine) allConfiguredCodes(ctx context.Context, scopeID, poolID, reservationID string) (map[uint8]bool, error) {
	codes := map[uint8]bool{}
	add := func(opts []*models.DHCPOption) {
		for _, o := range opts {
			if o.OptionCode >= 0 && o.OptionCode <= 255 {
				codes[uint8(o.OptionCode)] = true
			}
		}
	}

	global, err := e.store.GetGlobalOptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("dhcp: global options: %w", err)
	}
	add(global)

	if scopeID != "" {
		templates, err := e.store.GetTemplatesByScope(ctx, scopeID)
		if err != nil {
			return nil, fmt.Errorf("dhcp: templates for scope %s: %w", scopeID, err)
		}
		for _, t := range templates {
			tOpts, err := e.store.GetOptionsByTemplate(ctx, t.ID)
			if err != nil {
				e.log.Warn("dhcp: options for template failed, skipping", "template_id", t.ID, "error", err)
				continue
			}
			add(tOpts)
		}

		scopeOpts, err := e.store.GetOptionsByScope(ctx, scopeID)
		if err != nil {
			return nil, fmt.Errorf("dhcp: options for scope %s: %w", scopeID, err)
		}
		add(scopeOpts)
	}

	if poolID != "" {
		poolOpts, err := e.store.GetOptionsByPool(ctx, poolID)
		if err != nil {
			return nil, fmt.Errorf("dhcp: options for pool %s: %w", poolID, err)
		}
		add(poolOpts)
	}

	if reservationID != "" {
		resOpts, err := e.store.GetOptionsByReservation(ctx, reservationID)
		if err != nil {
			return nil, fmt.Errorf("dhcp: options for reservation %s: %w", reservationID, err)
		}
		add(resOpts)
	}

	return codes, nil
}

// catalogEntryFor returns opt's catalog entry, using the join the store is
// expected to have already populated (models.DHCPOption.Catalog) and
// falling back to a direct lookup otherwise.
func (e *Engine) catalogEntryFor(ctx context.Context, opt *models.DHCPOption) (*models.OptionCatalogEntry, error) {
	if opt.Catalog != nil {
		return opt.Catalog, nil
	}
	return e.store.GetOptionCatalogEntry(ctx, opt.OptionCode)
}

// encodeOptionValue encodes raw (a dhcp_option_values.value string) to wire
// format per vt, covering every value_type in the catalog CHECK constraint
// (amendment A5.4 adds fqdn and route_list to schema.sql's base set).
func encodeOptionValue(vt models.OptionValueType, raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	switch vt {
	case models.OptionValueTypeString:
		return []byte(raw), nil

	case models.OptionValueTypeUint8:
		v, err := strconv.ParseUint(raw, 10, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid uint8 value %q: %w", raw, err)
		}
		return []byte{byte(v)}, nil

	case models.OptionValueTypeUint16:
		v, err := strconv.ParseUint(raw, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid uint16 value %q: %w", raw, err)
		}
		return []byte{byte(v >> 8), byte(v)}, nil

	case models.OptionValueTypeUint32:
		v, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid uint32 value %q: %w", raw, err)
		}
		return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}, nil

	case models.OptionValueTypeBoolean:
		switch strings.ToLower(raw) {
		case "true", "1", "yes", "on":
			return []byte{1}, nil
		case "false", "0", "no", "off", "":
			return []byte{0}, nil
		default:
			return nil, fmt.Errorf("invalid boolean value %q", raw)
		}

	case models.OptionValueTypeIP:
		ip := net.ParseIP(raw)
		if ip == nil {
			return nil, fmt.Errorf("invalid ip value %q", raw)
		}
		v4 := ip.To4()
		if v4 == nil {
			return nil, fmt.Errorf("ip value %q is not IPv4", raw)
		}
		return v4, nil

	case models.OptionValueTypeIPList:
		parts := strings.Split(raw, ",")
		out := make([]byte, 0, len(parts)*net.IPv4len)
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			ip := net.ParseIP(p)
			if ip == nil {
				return nil, fmt.Errorf("invalid ip in ip_list %q", p)
			}
			v4 := ip.To4()
			if v4 == nil {
				return nil, fmt.Errorf("ip_list entry %q is not IPv4", p)
			}
			out = append(out, v4...)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("ip_list value %q has no addresses", raw)
		}
		return out, nil

	case models.OptionValueTypeBytes:
		cleaned := strings.NewReplacer(":", "", " ", "", "-", "").Replace(raw)
		cleaned = strings.TrimPrefix(strings.ToLower(cleaned), "0x")
		b, err := hex.DecodeString(cleaned)
		if err != nil {
			return nil, fmt.Errorf("invalid hex bytes value %q: %w", raw, err)
		}
		return b, nil

	case models.OptionValueTypeFQDN:
		name := strings.TrimSuffix(raw, ".")
		if name == "" {
			return nil, fmt.Errorf("empty fqdn value")
		}
		// rfc1035label.Labels.Labels holds whole dotted domain names, one
		// per slice element (it splits on "." and appends the trailing
		// zero-length terminator internally per element) — NOT
		// pre-split individual labels, which would double up the
		// terminator between every label.
		labels := &rfc1035label.Labels{Labels: []string{name}}
		return labels.ToBytes(), nil

	case models.OptionValueTypeRouteList:
		routes, err := parseRouteList(raw)
		if err != nil {
			return nil, err
		}
		return dhcpv4.Routes(routes).ToBytes(), nil

	default:
		return nil, fmt.Errorf("unsupported option value_type %q", vt)
	}
}

// parseRouteList parses the route_list wire-value convention gdhcp uses for
// RFC 3442 classless static routes: comma-separated "cidr:gateway" pairs,
// e.g. "10.0.1.0/24:10.0.0.1,0.0.0.0/0:10.0.0.1" (a default route is
// destination 0.0.0.0/0).
func parseRouteList(raw string) ([]*dhcpv4.Route, error) {
	entries := strings.Split(raw, ",")
	routes := make([]*dhcpv4.Route, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid route_list entry %q (want cidr:gateway)", entry)
		}
		_, dest, err := net.ParseCIDR(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, fmt.Errorf("invalid route_list destination %q: %w", parts[0], err)
		}
		gw := net.ParseIP(strings.TrimSpace(parts[1]))
		if gw == nil || gw.To4() == nil {
			return nil, fmt.Errorf("invalid route_list gateway %q", parts[1])
		}
		routes = append(routes, &dhcpv4.Route{Dest: dest, Router: gw.To4()})
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("route_list value %q has no routes", raw)
	}
	return routes, nil
}
