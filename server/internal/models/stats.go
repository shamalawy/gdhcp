package models

import "time"

// DHCPStats represents aggregated DHCP transaction counters for a scope over
// a time window (StatsRecorder.GetStats) or a single day
// (stats_dhcp_transactions table).
type DHCPStats struct {
	ScopeID       string `json:"scope_id"`
	DiscoverCount int64  `json:"discover_count"`
	OfferCount    int64  `json:"offer_count"`
	RequestCount  int64  `json:"request_count"`
	AckCount      int64  `json:"ack_count"`
	NakCount      int64  `json:"nak_count"`
	DeclineCount  int64  `json:"decline_count"`
	ReleaseCount  int64  `json:"release_count"`
	NoLeasesCount int64  `json:"no_leases_count"`
}

// UtilizationEntityType enumerates the entity kinds utilization is sampled
// against.
type UtilizationEntityType string

const (
	UtilizationEntityScope UtilizationEntityType = "scope"
	UtilizationEntityPool  UtilizationEntityType = "pool"
	UtilizationEntityVRF   UtilizationEntityType = "vrf"
	// UtilizationEntityPrefix covers every node of the IPAM tree. Its
	// meaning depends on the prefix's type: for aggregates and containers
	// it is how much of the block has been carved into child prefixes
	// (Total/Active count child blocks, not addresses); for subnets it is
	// host usage rolled up from the DHCP scope carved out of it.
	UtilizationEntityPrefix UtilizationEntityType = "prefix"
)

// Utilization is the current-point-in-time response shape for
// GET /api/v1/scopes/{id}/utilization and /api/v1/pools/{id}/utilization:
// total addresses, how many are actively leased, how many are reserved
// (but not necessarily leased), and the resulting percentage.
type Utilization struct {
	EntityType            UtilizationEntityType `json:"entity_type"`
	EntityID              string                `json:"entity_id"`
	Total                 int64                 `json:"total"`
	Active                int64                 `json:"active"`
	Reserved              int64                 `json:"reserved"`
	Available             int64                 `json:"available"`
	UtilizationPercentage float64               `json:"pct"`
}

// PrefixUtilization is the response shape for
// GET /api/v1/prefixes/{id}/utilization: a DHCP view (every scope carved
// anywhere under this prefix — this prefix itself or any descendant, walked
// via parent_id — summed across their pools, in the same
// {total,active,reserved,available,pct} shape GetScopeUtilization/
// GetPoolUtilization use, so the existing utilization-bar component renders
// it unchanged) plus an address-space view (how much of the prefix's own
// CIDR is covered by its direct, one-level child prefixes). Every prefix
// gets both views; a subnet with no scope anywhere beneath it reports a
// zeroed-out DHCP view rather than an error, alongside a still-valid space
// view.
type PrefixUtilization struct {
	PrefixID string `json:"prefix_id"`

	// DHCP view.
	Total                 int64   `json:"total"`
	Active                int64   `json:"active"`
	Reserved              int64   `json:"reserved"`
	Available             int64   `json:"available"`
	UtilizationPercentage float64 `json:"pct"`
	ScopeCount            int     `json:"scope_count"`

	// Address-space view.
	SpaceTotal       int64   `json:"space_total"`
	SpaceAllocated   int64   `json:"space_allocated"`
	SpacePercentage  float64 `json:"space_pct"`
	ChildPrefixCount int     `json:"child_prefix_count"`
}

// UtilizationSample is one row of stats_utilization_samples: a
// point-in-time snapshot, captured by StatsRecorder.SampleUtilization.
type UtilizationSample struct {
	ID                    int64                 `json:"id" db:"id"`
	SampleTimestamp       time.Time             `json:"sample_timestamp" db:"sample_timestamp"`
	EntityType            UtilizationEntityType `json:"entity_type" db:"entity_type"`
	EntityID              string                `json:"entity_id" db:"entity_id"`
	TotalAddresses        int64                 `json:"total_addresses" db:"total_addresses"`
	UsedAddresses         int64                 `json:"used_addresses" db:"used_addresses"`
	ReservedAddresses     int64                 `json:"reserved_addresses" db:"reserved_addresses"`
	AvailableAddresses    int64                 `json:"available_addresses" db:"available_addresses"`
	UtilizationPercentage float64               `json:"utilization_percentage" db:"utilization_percentage"`
}

// UtilizationHourly is one row of stats_utilization_hourly: the hourly
// min/max/avg rollup used for dashboard trend charts.
type UtilizationHourly struct {
	ID                       int64                 `json:"id" db:"id"`
	SampleHour               time.Time             `json:"sample_hour" db:"sample_hour"`
	EntityType               UtilizationEntityType `json:"entity_type" db:"entity_type"`
	EntityID                 string                `json:"entity_id" db:"entity_id"`
	TotalAddresses           int64                 `json:"total_addresses" db:"total_addresses"`
	UsedAddressesAvg         *float64              `json:"used_addresses_avg" db:"used_addresses_avg"`
	UsedAddressesMax         *int64                `json:"used_addresses_max" db:"used_addresses_max"`
	UsedAddressesMin         *int64                `json:"used_addresses_min" db:"used_addresses_min"`
	UtilizationPercentageAvg *float64              `json:"utilization_percentage_avg" db:"utilization_percentage_avg"`
	UtilizationPercentageMax *float64              `json:"utilization_percentage_max" db:"utilization_percentage_max"`
	UtilizationPercentageMin *float64              `json:"utilization_percentage_min" db:"utilization_percentage_min"`
}

// ChartPoint is one point on a GET /api/v1/dashboard/utilization-chart
// history series.
type ChartPoint struct {
	Timestamp             time.Time `json:"timestamp"`
	UtilizationPercentage float64   `json:"utilization_percentage"`
	UsedAddresses         int64     `json:"used_addresses"`
	TotalAddresses        int64     `json:"total_addresses"`
}

// DashboardStats is the response shape for GET /api/v1/dashboard/stats: the
// system-wide rollup shown on the landing page and pushed over the SSE
// `stats` event (amendment A12).
type DashboardStats struct {
	Timestamp                    time.Time                  `json:"timestamp"`
	TotalVRFs                    int64                      `json:"total_vrfs"`
	TotalScopes                  int64                      `json:"total_scopes"`
	TotalPools                   int64                      `json:"total_pools"`
	TotalAddresses               int64                      `json:"total_addresses"`
	TotalActiveLeases            int64                      `json:"total_active_leases"`
	TotalReservedAddresses       int64                      `json:"total_reserved_addresses"`
	TotalAvailableAddresses      int64                      `json:"total_available_addresses"`
	OverallUtilizationPercentage float64                    `json:"overall_utilization_percentage"`
	DHCPStatsWindow              *DHCPStats                 `json:"dhcp_stats_window,omitempty"`
	ClusterHealth                []*ClusterHealth           `json:"cluster_health,omitempty"`
	TopUtilizedScopes            []*ScopeUtilizationSummary `json:"top_utilized_scopes,omitempty"`
}

// ScopeUtilizationSummary is a compact scope+utilization pairing used in
// dashboard "top utilized" lists.
type ScopeUtilizationSummary struct {
	ScopeID               string  `json:"scope_id"`
	ScopeName             string  `json:"scope_name"`
	UtilizationPercentage float64 `json:"utilization_percentage"`
	Active                int64   `json:"active"`
	Total                 int64   `json:"total"`
}

// SearchResultType enumerates the entity kinds returned by global search.
type SearchResultType string

const (
	SearchResultVRF         SearchResultType = "vrf"
	SearchResultPrefix      SearchResultType = "prefix"
	SearchResultVLAN        SearchResultType = "vlan"
	SearchResultVLANGroup   SearchResultType = "vlan_group"
	SearchResultScope       SearchResultType = "scope"
	SearchResultPool        SearchResultType = "pool"
	SearchResultReservation SearchResultType = "reservation"
	SearchResultLease       SearchResultType = "lease"
	SearchResultTemplate    SearchResultType = "template"
	SearchResultOption      SearchResultType = "option"
	SearchResultCluster     SearchResultType = "cluster"
	SearchResultServer      SearchResultType = "server"
	SearchResultUser        SearchResultType = "user"
)

// SearchResult is one hit from GET /api/v1/search.
type SearchResult struct {
	Type        SearchResultType `json:"type"`
	EntityID    string           `json:"entity_id"`
	Title       string           `json:"title"`
	Subtitle    string           `json:"subtitle,omitempty"`
	Description string           `json:"description,omitempty"`
	URL         string           `json:"url"`
}
