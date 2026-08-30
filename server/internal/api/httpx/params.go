package httpx

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/store"
)

const (
	defaultLimit = 100
	maxLimit     = 1000
)

// ParseListParams reads the common list query parameters
// (docs/CONTRACTS.md Part 2: limit, offset, sort, search) plus any of the
// named filterKeys present in the query string, into a store.ListParams.
func ParseListParams(r *http.Request, filterKeys ...string) store.ListParams {
	q := r.URL.Query()

	limit := defaultLimit
	if s := q.Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	offset := 0
	if s := q.Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	var filters map[string]string
	for _, key := range filterKeys {
		if v := q.Get(key); v != "" {
			if filters == nil {
				filters = make(map[string]string, len(filterKeys))
			}
			filters[key] = v
		}
	}

	return store.ListParams{
		Limit:   limit,
		Offset:  offset,
		Sort:    q.Get("sort"),
		Search:  q.Get("search"),
		Filters: filters,
	}
}

// ParseWindow parses the "window" query parameter (a Go duration string,
// e.g. "1h", "24h") used by dashboard/stats-style endpoints, defaulting to
// def when absent or malformed.
func ParseWindow(r *http.Request, def time.Duration) time.Duration {
	s := r.URL.Query().Get("window")
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}

// ParseIntQuery reads an integer query parameter, defaulting to def when
// absent or malformed.
func ParseIntQuery(r *http.Request, key string, def int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// ParseBoolQuery reads a boolean query parameter ("true"/"false"), returning
// ok=false when the parameter is absent so callers can distinguish "unset"
// from "false".
func ParseBoolQuery(r *http.Request, key string) (value, ok bool) {
	s := r.URL.Query().Get(key)
	if s == "" {
		return false, false
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, false
	}
	return b, true
}

// ParseCIDR strictly parses a CIDR string, returning a 400 *APIError naming
// field on failure.
func ParseCIDR(field, s string) (*net.IPNet, error) {
	if strings.TrimSpace(s) == "" {
		return nil, Invalid(field+" is required", "field", field, "issue", "required")
	}
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return nil, Invalid(field+" must be a valid CIDR (e.g. 10.0.0.0/24)", "field", field, "issue", "invalid_cidr")
	}
	return ipnet, nil
}

// ParseIP strictly parses an IPv4/IPv6 address string. IPv4 results are
// normalized to net.IP's 4-byte form (net.ParseIP always returns the 16-byte
// IPv4-in-IPv6 form): left as-is, pgx's inet/cidr codec picks the family by
// slice length and would encode a "10.0.0.1" value as "::ffff:10.0.0.1",
// which then fails family-sensitive comparisons against IPv4 data (e.g. the
// dhcp_pools range-within-scope trigger, or an inet/cidr equality lookup).
func ParseIP(field, s string) (net.IP, error) {
	if strings.TrimSpace(s) == "" {
		return nil, Invalid(field+" is required", "field", field, "issue", "required")
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, Invalid(field+" must be a valid IP address", "field", field, "issue", "invalid_ip")
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip, nil
}

// ParseMAC strictly parses a hardware (MAC) address string.
func ParseMAC(field, s string) (net.HardwareAddr, error) {
	if strings.TrimSpace(s) == "" {
		return nil, Invalid(field+" is required", "field", field, "issue", "required")
	}
	mac, err := net.ParseMAC(s)
	if err != nil {
		return nil, Invalid(field+" must be a valid MAC address (e.g. aa:bb:cc:dd:ee:ff)", "field", field, "issue", "invalid_mac")
	}
	return mac, nil
}

// RequireString returns an INVALID_INPUT error if s is empty/whitespace.
func RequireString(field, s string) error {
	if strings.TrimSpace(s) == "" {
		return Invalid(field+" is required", "field", field, "issue", "required")
	}
	return nil
}
