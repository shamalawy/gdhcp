// postgres_cluster.go implements ClusterStore: cluster/server CRUD,
// heartbeat, election, and config-version bookkeeping for HA.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

const clusterSelectCols = `id, name, description, vip_address, vip_interface, vip_virtual_mac,
	heartbeat_interval_ms, heartbeat_ttl_ms, election_interval_ms, created_at, updated_at, created_by_user_id, updated_by_user_id`

func scanCluster(row pgx.Row) (*models.Cluster, error) {
	var c models.Cluster
	if err := row.Scan(&c.ID, &c.Name, &c.Description, &c.VIPAddress, &c.VIPInterface, &c.VIPVirtualMAC,
		&c.HeartbeatInterval, &c.HeartbeatTTL, &c.ElectionInterval, &c.CreatedAt, &c.UpdatedAt,
		&c.CreatedByUserID, &c.UpdatedByUserID); err != nil {
		return nil, err
	}
	return &c, nil
}

func (p *Postgres) GetClusterByID(ctx context.Context, clusterID string) (*models.Cluster, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+clusterSelectCols+` FROM clusters WHERE id = $1`, clusterID)
	c, err := scanCluster(row)
	if err != nil {
		return nil, mapReadError(err, "get cluster "+clusterID)
	}
	return c, nil
}

func (p *Postgres) ListClusters(ctx context.Context, params ListParams) (ListResult[*models.Cluster], error) {
	limit, offset := normalizeListParams(params)
	search := nilIfEmpty(params.Search)

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM clusters WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')`, search).Scan(&total); err != nil {
		return ListResult[*models.Cluster]{}, fmt.Errorf("store: count clusters: %w", err)
	}

	order := orderByClause(params.Sort, map[string]string{"name": "name", "created_at": "created_at"}, "name ASC")
	rows, err := p.pool.Query(ctx, `SELECT `+clusterSelectCols+` FROM clusters
		WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')
		ORDER BY `+order+` LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return ListResult[*models.Cluster]{}, fmt.Errorf("store: list clusters: %w", err)
	}
	defer rows.Close()

	var items []*models.Cluster
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return ListResult[*models.Cluster]{}, fmt.Errorf("store: list clusters: %w", err)
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.Cluster]{}, fmt.Errorf("store: list clusters: %w", err)
	}
	return ListResult[*models.Cluster]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) CreateCluster(ctx context.Context, c *models.Cluster) error {
	if c.ID == "" {
		c.ID = newUUID()
	}
	var vip any
	if c.VIPAddress != nil {
		vip = c.VIPAddress.String()
	}
	var vmac any
	if c.VIPVirtualMAC != nil {
		vmac = []byte(*c.VIPVirtualMAC)
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO clusters (id, name, description, vip_address, vip_interface, vip_virtual_mac,
			heartbeat_interval_ms, heartbeat_ttl_ms, election_interval_ms, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		RETURNING created_at, updated_at`,
		c.ID, c.Name, c.Description, vip, c.VIPInterface, vmac,
		c.HeartbeatInterval, c.HeartbeatTTL, c.ElectionInterval, c.CreatedByUserID,
	).Scan(&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create cluster")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) UpdateCluster(ctx context.Context, c *models.Cluster) error {
	var vip any
	if c.VIPAddress != nil {
		vip = c.VIPAddress.String()
	}
	var vmac any
	if c.VIPVirtualMAC != nil {
		vmac = []byte(*c.VIPVirtualMAC)
	}
	err := p.pool.QueryRow(ctx, `
		UPDATE clusters SET name=$2, description=$3, vip_address=$4, vip_interface=$5, vip_virtual_mac=$6,
			heartbeat_interval_ms=$7, heartbeat_ttl_ms=$8, election_interval_ms=$9, updated_by_user_id=$10, updated_at=now()
		WHERE id = $1 RETURNING updated_at`,
		c.ID, c.Name, c.Description, vip, c.VIPInterface, vmac,
		c.HeartbeatInterval, c.HeartbeatTTL, c.ElectionInterval, c.UpdatedByUserID,
	).Scan(&c.UpdatedAt)
	if err != nil {
		return mapWriteError(mapReadError(err, "update cluster "+c.ID), "update cluster")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) DeleteCluster(ctx context.Context, clusterID string) error {
	var serverCount int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM cluster_servers WHERE cluster_id = $1`, clusterID).Scan(&serverCount); err != nil {
		return fmt.Errorf("store: delete cluster %s: check servers: %w", clusterID, err)
	}
	if serverCount > 0 {
		return fmt.Errorf("store: delete cluster %s: %d registered servers: %w", clusterID, serverCount, models.ErrHasDependents)
	}
	tag, err := p.pool.Exec(ctx, `DELETE FROM clusters WHERE id = $1`, clusterID)
	if err != nil {
		return mapWriteError(err, "delete cluster")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete cluster %s: %w", clusterID, models.ErrNotFound)
	}
	p.notifyConfigChanged(ctx)
	return nil
}

const clusterServerSelectCols = `id, cluster_id, node_name, node_id, api_address, dhcp_address, created_at, updated_at`

func scanClusterServer(row pgx.Row) (*models.ClusterServer, error) {
	var s models.ClusterServer
	if err := row.Scan(&s.ID, &s.ClusterID, &s.NodeName, &s.NodeID, &s.APIAddress, &s.DHCPAddress, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func (p *Postgres) RegisterServer(ctx context.Context, server *models.ClusterServer) error {
	if server.ID == "" {
		server.ID = newUUID()
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO cluster_servers (id, cluster_id, node_name, node_id, api_address, dhcp_address)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (cluster_id, node_name) DO UPDATE SET
			node_id = EXCLUDED.node_id, api_address = EXCLUDED.api_address, dhcp_address = EXCLUDED.dhcp_address, updated_at = now()
		RETURNING id, created_at, updated_at`,
		server.ID, server.ClusterID, server.NodeName, server.NodeID, server.APIAddress, server.DHCPAddress,
	).Scan(&server.ID, &server.CreatedAt, &server.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "register server")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) DeregisterServer(ctx context.Context, clusterID, serverID string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM cluster_servers WHERE cluster_id = $1 AND id = $2`, clusterID, serverID)
	if err != nil {
		return mapWriteError(err, "deregister server")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: deregister server %s from cluster %s: %w", serverID, clusterID, models.ErrNotFound)
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) ListServers(ctx context.Context, clusterID string) ([]*models.ClusterServer, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+clusterServerSelectCols+` FROM cluster_servers WHERE cluster_id = $1 ORDER BY node_id`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("store: list servers in cluster %s: %w", clusterID, err)
	}
	defer rows.Close()

	var out []*models.ClusterServer
	for rows.Next() {
		s, err := scanClusterServer(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list servers in cluster %s: %w", clusterID, err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Heartbeat / election
// ---------------------------------------------------------------------------

const heartbeatSelectCols = `id, cluster_id, server_id, heartbeat_time, role, vip_assigned, lease_version, last_config_version, created_at, updated_at`

func scanHeartbeat(row pgx.Row) (*models.Heartbeat, error) {
	var h models.Heartbeat
	if err := row.Scan(&h.ID, &h.ClusterID, &h.ServerID, &h.HeartbeatTime, &h.Role, &h.VIPAssigned,
		&h.LeaseVersion, &h.LastConfigVersion, &h.CreatedAt, &h.UpdatedAt); err != nil {
		return nil, err
	}
	return &h, nil
}

// WriteHeartbeat upserts this server's heartbeat row: heartbeat_time =
// now(), role = 'PRIMARY' (docs/CONTRACTS.md Part 1's ClusterStore
// contract — the HA cluster manager calls this only from the server that
// currently holds primary role; see notes_for_integration). Returns
// models.ErrNotFound if serverID isn't registered in clusterID.
func (p *Postgres) WriteHeartbeat(ctx context.Context, clusterID, serverID string) error {
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cluster_servers WHERE id = $1 AND cluster_id = $2)`,
		serverID, clusterID).Scan(&exists); err != nil {
		return fmt.Errorf("store: write heartbeat: check server %s: %w", serverID, err)
	}
	if !exists {
		return fmt.Errorf("store: write heartbeat: server %s not registered in cluster %s: %w", serverID, clusterID, models.ErrNotFound)
	}

	_, err := p.pool.Exec(ctx, `
		INSERT INTO cluster_heartbeat (cluster_id, server_id, heartbeat_time, role, updated_at)
		VALUES ($1, $2, now(), 'PRIMARY', now())
		ON CONFLICT (cluster_id, server_id) DO UPDATE SET
			heartbeat_time = now(), role = 'PRIMARY', updated_at = now()`,
		clusterID, serverID)
	if err != nil {
		return mapWriteError(err, "write heartbeat")
	}
	return nil
}

func (p *Postgres) GetHeartbeats(ctx context.Context, clusterID string) ([]*models.Heartbeat, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+heartbeatSelectCols+` FROM cluster_heartbeat WHERE cluster_id = $1 ORDER BY heartbeat_time DESC`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("store: get heartbeats for cluster %s: %w", clusterID, err)
	}
	defer rows.Close()

	var out []*models.Heartbeat
	for rows.Next() {
		h, err := scanHeartbeat(rows)
		if err != nil {
			return nil, fmt.Errorf("store: get heartbeats for cluster %s: %w", clusterID, err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ElectionQuery returns the lowest node_id among servers in clusterID whose
// heartbeat is fresher than the cluster's heartbeat_ttl_ms.
func (p *Postgres) ElectionQuery(ctx context.Context, clusterID string) (string, error) {
	var serverID string
	err := p.pool.QueryRow(ctx, `
		SELECT cs.id
		FROM cluster_servers cs
		JOIN cluster_heartbeat ch ON ch.server_id = cs.id AND ch.cluster_id = cs.cluster_id
		JOIN clusters c ON c.id = cs.cluster_id
		WHERE cs.cluster_id = $1
			AND ch.heartbeat_time > now() - (c.heartbeat_ttl_ms || ' milliseconds')::interval
		ORDER BY cs.node_id ASC
		LIMIT 1`, clusterID).Scan(&serverID)
	if err != nil {
		return "", mapReadError(err, "election query for cluster "+clusterID)
	}
	return serverID, nil
}

func (p *Postgres) UpdateLeaseVersion(ctx context.Context, clusterID, serverID string) (int, error) {
	var version int
	err := p.pool.QueryRow(ctx, `
		UPDATE cluster_heartbeat SET lease_version = lease_version + 1, updated_at = now()
		WHERE cluster_id = $1 AND server_id = $2
		RETURNING lease_version`, clusterID, serverID).Scan(&version)
	if err != nil {
		return 0, mapReadError(err, fmt.Sprintf("update lease version for server %s", serverID))
	}
	return version, nil
}

func (p *Postgres) UpdateLastConfigVersion(ctx context.Context, clusterID, serverID string, version int) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE cluster_heartbeat SET last_config_version = $3, updated_at = now()
		WHERE cluster_id = $1 AND server_id = $2`, clusterID, serverID, version)
	if err != nil {
		return mapWriteError(err, "update last config version")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: update last config version for server %s: %w", serverID, models.ErrNotFound)
	}
	return nil
}

// GetClusterHealth computes each server's role/VIP/heartbeat freshness and
// an overall cluster_state. LocalServerID/LocalRole are left zero-valued:
// "local" is relative to the calling server, which this cluster-scoped
// query has no way to know — the API/HA layer (which holds its own
// config.HAConfig.ServerID) should overlay those two fields. See
// notes_for_integration.
func (p *Postgres) GetClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error) {
	cluster, err := p.GetClusterByID(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("store: get cluster health: %w", err)
	}

	rows, err := p.pool.Query(ctx, `
		SELECT cs.id, cs.node_name, cs.node_id, cs.api_address,
			COALESCE(ch.role, 'SECONDARY'), COALESCE(ch.vip_assigned, false),
			ch.heartbeat_time, COALESCE(ch.last_config_version, 0)
		FROM cluster_servers cs
		LEFT JOIN cluster_heartbeat ch ON ch.server_id = cs.id AND ch.cluster_id = cs.cluster_id
		WHERE cs.cluster_id = $1
		ORDER BY cs.node_id`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("store: get cluster health: list servers: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	ttl := time.Duration(cluster.HeartbeatTTL) * time.Millisecond
	var servers []*models.ClusterServerHealth
	healthyCount := 0
	for rows.Next() {
		var sh models.ClusterServerHealth
		var heartbeatTime *time.Time
		if err := rows.Scan(&sh.ServerID, &sh.NodeName, &sh.NodeID, &sh.APIAddress, &sh.Role, &sh.VIPAssigned,
			&heartbeatTime, &sh.LastConfigApply); err != nil {
			return nil, fmt.Errorf("store: get cluster health: scan server: %w", err)
		}
		if heartbeatTime != nil {
			sh.HeartbeatTime = *heartbeatTime
			sh.HeartbeatAgeMs = now.Sub(*heartbeatTime).Milliseconds()
			sh.IsHealthy = now.Sub(*heartbeatTime) <= ttl
		}
		if sh.IsHealthy {
			healthyCount++
		}
		servers = append(servers, &sh)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get cluster health: %w", err)
	}

	state := models.ClusterStateDown
	switch {
	case healthyCount == len(servers) && len(servers) > 0:
		state = models.ClusterStateHealthy
	case healthyCount > 0:
		state = models.ClusterStateDegraded
	}

	return &models.ClusterHealth{
		ClusterID:    clusterID,
		Timestamp:    now,
		Servers:      servers,
		ClusterState: state,
	}, nil
}
