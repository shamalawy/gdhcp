package client

import (
	"context"
	"fmt"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// ClusterCreateRequest is the request body for POST /clusters.
type ClusterCreateRequest struct {
	Name                string  `json:"name"`
	Description         *string `json:"description,omitempty"`
	VIPAddress          string  `json:"vip_address"`
	VIPInterface        string  `json:"vip_interface"`
	HeartbeatIntervalMs int     `json:"heartbeat_interval_ms"`
	HeartbeatTTLMs      int     `json:"heartbeat_ttl_ms,omitempty"`
	ElectionIntervalMs  int     `json:"election_interval_ms,omitempty"`
}

// ClusterUpdateRequest is the request body for PUT /clusters/{cluster_id}.
type ClusterUpdateRequest struct {
	Name                *string `json:"name,omitempty"`
	Description         *string `json:"description,omitempty"`
	HeartbeatIntervalMs *int    `json:"heartbeat_interval_ms,omitempty"`
	HeartbeatTTLMs      *int    `json:"heartbeat_ttl_ms,omitempty"`
	ElectionIntervalMs  *int    `json:"election_interval_ms,omitempty"`
}

// ClusterServerCreateRequest is the request body for
// POST /clusters/{cluster_id}/servers.
type ClusterServerCreateRequest struct {
	NodeName    string `json:"node_name"`
	NodeID      int    `json:"node_id"`
	APIAddress  string `json:"api_address"`
	DHCPAddress string `json:"dhcp_address"`
}

// ListClusters returns GET /clusters.
func (c *Client) ListClusters(ctx context.Context, p ListParams) ([]*models.Cluster, error) {
	var clusters []*models.Cluster
	if err := c.get(ctx, "/api/v1/clusters", p.Values(), &clusters); err != nil {
		return nil, err
	}
	return clusters, nil
}

// GetCluster returns GET /clusters/{cluster_id}.
func (c *Client) GetCluster(ctx context.Context, id string) (*models.Cluster, error) {
	var cl models.Cluster
	if err := c.get(ctx, "/api/v1/clusters/"+id, nil, &cl); err != nil {
		return nil, err
	}
	return &cl, nil
}

// CreateCluster returns POST /clusters.
func (c *Client) CreateCluster(ctx context.Context, req ClusterCreateRequest) (*models.Cluster, error) {
	var cl models.Cluster
	if err := c.post(ctx, "/api/v1/clusters", req, &cl); err != nil {
		return nil, err
	}
	return &cl, nil
}

// UpdateCluster returns PUT /clusters/{cluster_id}.
func (c *Client) UpdateCluster(ctx context.Context, id string, req ClusterUpdateRequest) (*models.Cluster, error) {
	var cl models.Cluster
	if err := c.put(ctx, "/api/v1/clusters/"+id, req, &cl); err != nil {
		return nil, err
	}
	return &cl, nil
}

// DeleteCluster returns DELETE /clusters/{cluster_id}.
func (c *Client) DeleteCluster(ctx context.Context, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/clusters/%s", id))
}

// ClusterServers returns GET /clusters/{cluster_id}/servers.
func (c *Client) ClusterServers(ctx context.Context, clusterID string) ([]*models.ClusterServer, error) {
	var servers []*models.ClusterServer
	if err := c.get(ctx, fmt.Sprintf("/api/v1/clusters/%s/servers", clusterID), nil, &servers); err != nil {
		return nil, err
	}
	return servers, nil
}

// RegisterClusterServer returns POST /clusters/{cluster_id}/servers.
func (c *Client) RegisterClusterServer(ctx context.Context, clusterID string, req ClusterServerCreateRequest) (*models.ClusterServer, error) {
	var s models.ClusterServer
	if err := c.post(ctx, fmt.Sprintf("/api/v1/clusters/%s/servers", clusterID), req, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// DeregisterClusterServer returns DELETE /clusters/{cluster_id}/servers/{server_id}.
func (c *Client) DeregisterClusterServer(ctx context.Context, clusterID, serverID string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/clusters/%s/servers/%s", clusterID, serverID))
}

// ClusterHealth returns GET /clusters/{cluster_id}/health.
func (c *Client) ClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error) {
	var h models.ClusterHealth
	if err := c.get(ctx, fmt.Sprintf("/api/v1/clusters/%s/health", clusterID), nil, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// ClusterHeartbeats returns GET /clusters/{cluster_id}/heartbeats.
func (c *Client) ClusterHeartbeats(ctx context.Context, clusterID string) ([]*models.Heartbeat, error) {
	var hbs []*models.Heartbeat
	if err := c.get(ctx, fmt.Sprintf("/api/v1/clusters/%s/heartbeats", clusterID), nil, &hbs); err != nil {
		return nil, err
	}
	return hbs, nil
}

// FailoverResponse is the response shape for POST /clusters/{cluster_id}/failover.
type FailoverResponse struct {
	Message    string `json:"message"`
	NewPrimary string `json:"new_primary"`
}

// Failover returns POST /clusters/{cluster_id}/failover, manually
// triggering a role change to targetServerID.
func (c *Client) Failover(ctx context.Context, clusterID, targetServerID string) (*FailoverResponse, error) {
	req := struct {
		TargetServerID string `json:"target_server_id"`
	}{TargetServerID: targetServerID}
	var resp FailoverResponse
	if err := c.post(ctx, fmt.Sprintf("/api/v1/clusters/%s/failover", clusterID), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// BootstrapCluster returns POST /clusters/{cluster_id}/bootstrap.
func (c *Client) BootstrapCluster(ctx context.Context, clusterID string, serverIDs []string) (string, error) {
	req := struct {
		ServerIDs []string `json:"server_ids"`
	}{ServerIDs: serverIDs}
	var resp messageResponse
	if err := c.post(ctx, fmt.Sprintf("/api/v1/clusters/%s/bootstrap", clusterID), req, &resp); err != nil {
		return "", err
	}
	return resp.Message, nil
}
