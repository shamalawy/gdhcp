package models

import (
	"net"
	"time"
)

// ClusterRole is the HA role a cluster server currently holds.
type ClusterRole string

const (
	ClusterRolePrimary   ClusterRole = "PRIMARY"
	ClusterRoleSecondary ClusterRole = "SECONDARY"
)

// ClusterState summarizes the overall health of a cluster for the dashboard
// and GET /api/v1/clusters/{id}/health.
type ClusterState string

const (
	ClusterStateHealthy  ClusterState = "HEALTHY"
	ClusterStateDegraded ClusterState = "DEGRADED"
	ClusterStateDown     ClusterState = "DOWN"
)

// Cluster represents an HA cluster (clusters table): the VIP it owns and the
// heartbeat/election timing that governs failover.
type Cluster struct {
	ID                string        `json:"id" db:"id"`
	Name              string        `json:"name" db:"name"`
	Description       *string       `json:"description" db:"description"`
	VIPAddress        *net.IP       `json:"vip_address" db:"vip_address"`
	VIPInterface      *string       `json:"vip_interface" db:"vip_interface"`
	VIPVirtualMAC     *HardwareAddr `json:"vip_virtual_mac" db:"vip_virtual_mac"`
	HeartbeatInterval int           `json:"heartbeat_interval_ms" db:"heartbeat_interval_ms"`
	HeartbeatTTL      int           `json:"heartbeat_ttl_ms" db:"heartbeat_ttl_ms"`
	ElectionInterval  int           `json:"election_interval_ms" db:"election_interval_ms"`
	CreatedAt         time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at" db:"updated_at"`
	CreatedByUserID   *int64        `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID   *int64        `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
}

// ClusterServer represents one server registered in a cluster
// (cluster_servers table). NodeID is the election tie-breaker: lower wins.
type ClusterServer struct {
	ID          string    `json:"id" db:"id"`
	ClusterID   string    `json:"cluster_id" db:"cluster_id"`
	NodeName    string    `json:"node_name" db:"node_name"`
	NodeID      int       `json:"node_id" db:"node_id"`
	APIAddress  string    `json:"api_address" db:"api_address"`
	DHCPAddress string    `json:"dhcp_address" db:"dhcp_address"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Heartbeat represents a cluster server's latest heartbeat row
// (cluster_heartbeat table). LeaseVersion/LastConfigVersion drive config
// propagation: the primary bumps LeaseVersion on config change, and each
// secondary records the version it has applied.
type Heartbeat struct {
	ID                string      `json:"id" db:"id"`
	ClusterID         string      `json:"cluster_id" db:"cluster_id"`
	ServerID          string      `json:"server_id" db:"server_id"`
	HeartbeatTime     time.Time   `json:"heartbeat_time" db:"heartbeat_time"`
	Role              ClusterRole `json:"role" db:"role"`
	VIPAssigned       bool        `json:"vip_assigned" db:"vip_assigned"`
	LeaseVersion      int         `json:"lease_version" db:"lease_version"`
	LastConfigVersion int         `json:"last_config_version" db:"last_config_version"`
	CreatedAt         time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at" db:"updated_at"`
}

// ClusterServerHealth is one server's row within a ClusterHealth response.
type ClusterServerHealth struct {
	ServerID        string      `json:"server_id"`
	NodeName        string      `json:"node_name"`
	NodeID          int         `json:"node_id"`
	Role            ClusterRole `json:"role"`
	VIPAssigned     bool        `json:"vip_assigned"`
	HeartbeatTime   time.Time   `json:"heartbeat_time"`
	HeartbeatAgeMs  int64       `json:"heartbeat_age_ms"`
	IsHealthy       bool        `json:"is_healthy"`
	APIAddress      string      `json:"api_address"`
	LastConfigApply int         `json:"last_config_version"`
}

// ClusterHealth is the response shape for GET /api/v1/clusters/{id}/health
// and feeds the dashboard's cluster status widget.
type ClusterHealth struct {
	ClusterID     string                 `json:"cluster_id"`
	LocalServerID string                 `json:"local_server_id"`
	LocalRole     ClusterRole            `json:"local_role"`
	Timestamp     time.Time              `json:"timestamp"`
	Servers       []*ClusterServerHealth `json:"servers"`
	ClusterState  ClusterState           `json:"cluster_state"`
	Notes         string                 `json:"notes"`
}
