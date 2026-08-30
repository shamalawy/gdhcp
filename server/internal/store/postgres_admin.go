// postgres_admin.go implements the remaining CRUD surfaces: ScopeStore and
// PoolStore's Create/Update/Delete/List (utilization lives in
// postgres_stats.go), TagStore, AuditStore, and SearchStore's global search.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// ---------------------------------------------------------------------------
// ScopeStore
// ---------------------------------------------------------------------------

func (p *Postgres) ListScopes(ctx context.Context, params ListParams) (ListResult[*models.DHCPScope], error) {
	limit, offset := normalizeListParams(params)
	vrfID := nilIfEmpty(params.Filters["vrf_id"])
	clusterID := nilIfEmpty(params.Filters["cluster_id"])
	// Lets a prefix page ask "is there already a scope on me?" in one call,
	// which is what the IPAM -> DHCP cross-link needs.
	prefixID := nilIfEmpty(params.Filters["ipam_prefix_id"])
	search := nilIfEmpty(params.Search)

	const where = `WHERE ($1::uuid IS NULL OR vrf_id = $1)
		AND ($2::uuid IS NULL OR cluster_id = $2)
		AND ($3::text IS NULL OR name ILIKE '%'||$3||'%')
		AND ($4::uuid IS NULL OR ipam_prefix_id = $4)`

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_scopes `+where, vrfID, clusterID, search, prefixID).Scan(&total); err != nil {
		return ListResult[*models.DHCPScope]{}, fmt.Errorf("store: count scopes: %w", err)
	}

	order := orderByClause(params.Sort, map[string]string{"name": "name", "created_at": "created_at"}, "name ASC")
	rows, err := p.pool.Query(ctx, `SELECT `+scopeSelectCols+` FROM dhcp_scopes `+where+
		` ORDER BY `+order+` LIMIT $5 OFFSET $6`, vrfID, clusterID, search, prefixID, limit, offset)
	if err != nil {
		return ListResult[*models.DHCPScope]{}, fmt.Errorf("store: list scopes: %w", err)
	}
	defer rows.Close()

	items, err := collectScopes(rows)
	if err != nil {
		return ListResult[*models.DHCPScope]{}, fmt.Errorf("store: list scopes: %w", err)
	}
	return ListResult[*models.DHCPScope]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) CreateScope(ctx context.Context, s *models.DHCPScope) error {
	if s.ID == "" {
		s.ID = newUUID()
	}
	// network is cached from the backing IPAM prefix (docs/db/schema.sql
	// dhcp_scopes.network comment) so it can never drift from it.
	prefix, err := p.GetPrefixByID(ctx, s.IPAMPrefixID)
	if err != nil {
		return fmt.Errorf("store: create scope: load ipam prefix %s: %w", s.IPAMPrefixID, err)
	}
	s.Network = prefix.Prefix

	err = p.pool.QueryRow(ctx, `
		INSERT INTO dhcp_scopes (id, vrf_id, cluster_id, ipam_prefix_id, name, description, network, is_enabled, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		RETURNING created_at, updated_at`,
		s.ID, s.VRFID, s.ClusterID, s.IPAMPrefixID, s.Name, s.Description, s.Network.String(), s.IsEnabled, s.CreatedByUserID,
	).Scan(&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create scope")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) UpdateScope(ctx context.Context, s *models.DHCPScope) error {
	err := p.pool.QueryRow(ctx, `
		UPDATE dhcp_scopes SET name=$2, description=$3, is_enabled=$4, updated_by_user_id=$5, updated_at=now()
		WHERE id = $1 RETURNING updated_at`,
		s.ID, s.Name, s.Description, s.IsEnabled, s.UpdatedByUserID,
	).Scan(&s.UpdatedAt)
	if err != nil {
		return mapWriteError(mapReadError(err, "update scope "+s.ID), "update scope")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) DeleteScope(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM dhcp_scopes WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete scope")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete scope %s: %w", id, models.ErrNotFound)
	}
	p.notifyConfigChanged(ctx)
	return nil
}

// ---------------------------------------------------------------------------
// PoolStore
// ---------------------------------------------------------------------------

func (p *Postgres) ListPoolsByScope(ctx context.Context, scopeID string, params ListParams) (ListResult[*models.DHCPPool], error) {
	limit, offset := normalizeListParams(params)

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_pools WHERE scope_id = $1`, scopeID).Scan(&total); err != nil {
		return ListResult[*models.DHCPPool]{}, fmt.Errorf("store: count pools in scope %s: %w", scopeID, err)
	}

	order := orderByClause(params.Sort, map[string]string{"name": "name", "priority": "priority"}, "priority DESC, name ASC")
	rows, err := p.pool.Query(ctx, `SELECT `+poolSelectCols+` FROM dhcp_pools WHERE scope_id = $1
		ORDER BY `+order+` LIMIT $2 OFFSET $3`, scopeID, limit, offset)
	if err != nil {
		return ListResult[*models.DHCPPool]{}, fmt.Errorf("store: list pools in scope %s: %w", scopeID, err)
	}
	defer rows.Close()

	var items []*models.DHCPPool
	for rows.Next() {
		pl, err := scanPool(rows)
		if err != nil {
			return ListResult[*models.DHCPPool]{}, fmt.Errorf("store: list pools in scope %s: %w", scopeID, err)
		}
		items = append(items, pl)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.DHCPPool]{}, fmt.Errorf("store: list pools in scope %s: %w", scopeID, err)
	}
	return ListResult[*models.DHCPPool]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) CreatePool(ctx context.Context, pl *models.DHCPPool) error {
	if pl.ID == "" {
		pl.ID = newUUID()
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO dhcp_pools (id, scope_id, name, description, range_start, range_end, is_enabled, priority, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		RETURNING created_at, updated_at`,
		pl.ID, pl.ScopeID, pl.Name, pl.Description, pl.RangeStart, pl.RangeEnd, pl.IsEnabled, pl.Priority, pl.CreatedByUserID,
	).Scan(&pl.CreatedAt, &pl.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create pool")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) UpdatePool(ctx context.Context, pl *models.DHCPPool) error {
	err := p.pool.QueryRow(ctx, `
		UPDATE dhcp_pools SET name=$2, description=$3, range_start=$4, range_end=$5, is_enabled=$6, priority=$7,
			updated_by_user_id=$8, updated_at=now()
		WHERE id = $1 RETURNING updated_at`,
		pl.ID, pl.Name, pl.Description, pl.RangeStart, pl.RangeEnd, pl.IsEnabled, pl.Priority, pl.UpdatedByUserID,
	).Scan(&pl.UpdatedAt)
	if err != nil {
		return mapWriteError(mapReadError(err, "update pool "+pl.ID), "update pool")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) DeletePool(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM dhcp_pools WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete pool")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete pool %s: %w", id, models.ErrNotFound)
	}
	p.notifyConfigChanged(ctx)
	return nil
}

// ---------------------------------------------------------------------------
// TagStore
// ---------------------------------------------------------------------------

func (p *Postgres) ListTags(ctx context.Context, params ListParams) (ListResult[*models.Tag], error) {
	limit, offset := normalizeListParams(params)
	search := nilIfEmpty(params.Search)

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM tags WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')`, search).Scan(&total); err != nil {
		return ListResult[*models.Tag]{}, fmt.Errorf("store: count tags: %w", err)
	}

	rows, err := p.pool.Query(ctx, `SELECT id, name, color, description, created_at, created_by_user_id FROM tags
		WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')
		ORDER BY name LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return ListResult[*models.Tag]{}, fmt.Errorf("store: list tags: %w", err)
	}
	defer rows.Close()

	var items []*models.Tag
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return ListResult[*models.Tag]{}, fmt.Errorf("store: list tags: %w", err)
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.Tag]{}, fmt.Errorf("store: list tags: %w", err)
	}
	return ListResult[*models.Tag]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func scanTag(row pgx.Row) (*models.Tag, error) {
	var t models.Tag
	if err := row.Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt, &t.CreatedByUserID); err != nil {
		return nil, err
	}
	return &t, nil
}

func (p *Postgres) CreateTag(ctx context.Context, t *models.Tag) error {
	if t.ID == "" {
		t.ID = newUUID()
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO tags (id, name, color, description, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		t.ID, t.Name, t.Color, t.Description, t.CreatedByUserID,
	).Scan(&t.CreatedAt)
	if err != nil {
		return mapWriteError(err, "create tag")
	}
	return nil
}

func (p *Postgres) DeleteTag(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM tags WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete tag")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete tag %s: %w", id, models.ErrNotFound)
	}
	return nil
}

func (p *Postgres) AddEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) (*models.EntityTag, error) {
	et := &models.EntityTag{ID: newUUID(), TagID: tagID, EntityType: entityType, EntityID: entityID}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO entity_tags (id, tag_id, entity_type, entity_id) VALUES ($1, $2, $3, $4)
		ON CONFLICT (tag_id, entity_type, entity_id) DO UPDATE SET tag_id = EXCLUDED.tag_id
		RETURNING id, created_at`, et.ID, tagID, entityType, entityID).Scan(&et.ID, &et.CreatedAt)
	if err != nil {
		return nil, mapWriteError(err, "add entity tag")
	}
	return et, nil
}

func (p *Postgres) RemoveEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM entity_tags WHERE tag_id = $1 AND entity_type = $2 AND entity_id = $3`,
		tagID, entityType, entityID)
	if err != nil {
		return mapWriteError(err, "remove entity tag")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: remove entity tag %s from %s %s: %w", tagID, entityType, entityID, models.ErrNotFound)
	}
	return nil
}

func (p *Postgres) ListTagsForEntity(ctx context.Context, entityType models.TaggableEntityType, entityID string) ([]*models.Tag, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT t.id, t.name, t.color, t.description, t.created_at, t.created_by_user_id
		FROM entity_tags et JOIN tags t ON t.id = et.tag_id
		WHERE et.entity_type = $1 AND et.entity_id = $2
		ORDER BY t.name`, entityType, entityID)
	if err != nil {
		return nil, fmt.Errorf("store: list tags for %s %s: %w", entityType, entityID, err)
	}
	defer rows.Close()

	var out []*models.Tag
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list tags for %s %s: %w", entityType, entityID, err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// AuditStore
// ---------------------------------------------------------------------------

func (p *Postgres) LogAction(ctx context.Context, log *models.AuditLog) error {
	var changesJSON any
	if len(log.Changes) > 0 {
		b, err := json.Marshal(log.Changes)
		if err != nil {
			return fmt.Errorf("store: log action: marshal changes: %w", err)
		}
		changesJSON = string(b)
	}

	err := p.pool.QueryRow(ctx, `
		INSERT INTO audit_log (user_id, action, entity_type, entity_id, entity_name, changes, ip_address, user_agent, description)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)
		RETURNING id, timestamp`,
		log.UserID, log.Action, log.EntityType, log.EntityID, log.EntityName, changesJSON, log.IPAddress, log.UserAgent, log.Description,
	).Scan(&log.ID, &log.Timestamp)
	if err != nil {
		return mapWriteError(err, "log action")
	}
	return nil
}

func (p *Postgres) ListAuditLog(ctx context.Context, offset, limit int, filters map[string]interface{}) ([]*models.AuditLog, int64, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	entityType := nilIfEmpty(fmt.Sprint(filters["entity_type"]))
	if filters["entity_type"] == nil {
		entityType = nil
	}
	entityID := nilIfEmpty(fmt.Sprint(filters["entity_id"]))
	if filters["entity_id"] == nil {
		entityID = nil
	}
	action := nilIfEmpty(fmt.Sprint(filters["action"]))
	if filters["action"] == nil {
		action = nil
	}
	var userID any
	if v, ok := filters["user_id"]; ok {
		userID = v
	}

	const where = `WHERE ($1::text IS NULL OR entity_type = $1)
		AND ($2::text IS NULL OR entity_id = $2)
		AND ($3::text IS NULL OR action = $3)
		AND ($4::bigint IS NULL OR user_id = $4)`

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log `+where, entityType, entityID, action, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count audit log: %w", err)
	}

	rows, err := p.pool.Query(ctx, `
		SELECT a.id, a.user_id, a.action, a.entity_type, a.entity_id, a.entity_name, a.changes, a.ip_address,
			a.user_agent, a.timestamp, a.description, u.username, u.email, u.role, u.is_active
		FROM audit_log a LEFT JOIN users u ON u.id = a.user_id
		`+where+`
		ORDER BY a.timestamp DESC LIMIT $5 OFFSET $6`, entityType, entityID, action, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list audit log: %w", err)
	}
	defer rows.Close()

	var out []*models.AuditLog
	for rows.Next() {
		var a models.AuditLog
		var changesRaw []byte
		var ipAddr net.IP
		var username, email *string
		var role *models.Role
		var isActive *bool

		if err := rows.Scan(&a.ID, &a.UserID, &a.Action, &a.EntityType, &a.EntityID, &a.EntityName, &changesRaw,
			&ipAddr, &a.UserAgent, &a.Timestamp, &a.Description, &username, &email, &role, &isActive); err != nil {
			return nil, 0, fmt.Errorf("store: list audit log: scan: %w", err)
		}
		if len(ipAddr) > 0 {
			s := ipAddr.String()
			a.IPAddress = &s
		}
		if len(changesRaw) > 0 {
			if err := json.Unmarshal(changesRaw, &a.Changes); err != nil {
				return nil, 0, fmt.Errorf("store: list audit log: unmarshal changes: %w", err)
			}
		}
		if username != nil {
			a.User = &models.User{ID: derefInt64(a.UserID), Username: *username}
			if email != nil {
				a.User.Email = *email
			}
			if role != nil {
				a.User.Role = *role
			}
			if isActive != nil {
				a.User.IsActive = *isActive
			}
		}
		out = append(out, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: list audit log: %w", err)
	}
	return out, total, nil
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// ---------------------------------------------------------------------------
// SearchStore
// ---------------------------------------------------------------------------

// GlobalSearch matches query as an IP/CIDR (inet containment) when it
// parses as one, otherwise as a case-insensitive substring across
// name/hostname/description/binding-key/tag-name fields, across the ten
// SearchResultType entity kinds in one UNION query.
func (p *Postgres) GlobalSearch(ctx context.Context, query string, entityTypes []string, limit int) ([]*models.SearchResult, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	var ipArg any
	var qArg any = query
	if ip, ok := parseSearchIP(query); ok {
		ipArg = ip
		qArg = nil
	}

	var typesArg any
	if len(entityTypes) > 0 {
		typesArg = entityTypes
	}

	rows, err := p.pool.Query(ctx, globalSearchSQL, ipArg, qArg, typesArg, limit)
	if err != nil {
		return nil, fmt.Errorf("store: global search: %w", err)
	}
	defer rows.Close()

	var out []*models.SearchResult
	for rows.Next() {
		var r models.SearchResult
		if err := rows.Scan(&r.Type, &r.EntityID, &r.Title, &r.Subtitle, &r.Description, &r.URL); err != nil {
			return nil, fmt.Errorf("store: global search: scan: %w", err)
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// parseSearchIP parses q as a bare address or CIDR, returning it as a
// net.IPNet CIDR string suitable for binding to an ::inet parameter (a bare
// address becomes a /32 or /128 host route).
func parseSearchIP(q string) (string, bool) {
	if strings.Contains(q, "/") {
		p, err := netip.ParsePrefix(q)
		if err != nil {
			return "", false
		}
		return p.String(), true
	}
	a, err := netip.ParseAddr(q)
	if err != nil {
		return "", false
	}
	return netip.PrefixFrom(a, a.BitLen()).String(), true
}

const globalSearchSQL = `
WITH matches AS (
	SELECT 'vrf' AS type, v.id::text AS entity_id, v.name AS title,
		COALESCE(v.route_distinguisher, '') AS subtitle, COALESCE(v.description, '') AS description,
		'/vrfs/' || v.id AS url
	FROM vrfs v
	WHERE $1::inet IS NULL AND $2::text IS NOT NULL AND (
		v.name ILIKE '%' || $2 || '%' OR COALESCE(v.description, '') ILIKE '%' || $2 || '%'
		OR EXISTS (SELECT 1 FROM entity_tags et JOIN tags t ON t.id = et.tag_id WHERE et.entity_type = 'vrf' AND et.entity_id = v.id AND t.name ILIKE '%' || $2 || '%')
	)

	UNION ALL
	SELECT 'prefix', pfx.id::text, COALESCE(pfx.name, host(pfx.prefix)), pfx.prefix_type::text,
		COALESCE(pfx.description, ''), '/ipam/prefixes/' || pfx.id
	FROM ipam_prefixes pfx
	WHERE ($1::inet IS NOT NULL AND pfx.prefix && $1)
		OR ($1::inet IS NULL AND $2::text IS NOT NULL AND (COALESCE(pfx.name, '') ILIKE '%' || $2 || '%' OR COALESCE(pfx.description, '') ILIKE '%' || $2 || '%'))

	UNION ALL
	SELECT 'scope', sc.id::text, sc.name, host(sc.network::inet), COALESCE(sc.description, ''), '/scopes/' || sc.id
	FROM dhcp_scopes sc
	WHERE ($1::inet IS NOT NULL AND sc.network && $1)
		OR ($1::inet IS NULL AND $2::text IS NOT NULL AND (
			sc.name ILIKE '%' || $2 || '%' OR COALESCE(sc.description, '') ILIKE '%' || $2 || '%'
			OR EXISTS (SELECT 1 FROM entity_tags et JOIN tags t ON t.id = et.tag_id WHERE et.entity_type = 'scope' AND et.entity_id = sc.id AND t.name ILIKE '%' || $2 || '%')
		))

	UNION ALL
	SELECT 'pool', po.id::text, po.name, host(po.range_start) || ' - ' || host(po.range_end), COALESCE(po.description, ''), '/pools/' || po.id
	FROM dhcp_pools po
	WHERE ($1::inet IS NOT NULL AND po.range_start <= $1 AND $1 <= po.range_end)
		OR ($1::inet IS NULL AND $2::text IS NOT NULL AND (
			po.name ILIKE '%' || $2 || '%' OR COALESCE(po.description, '') ILIKE '%' || $2 || '%'
			OR EXISTS (SELECT 1 FROM entity_tags et JOIN tags t ON t.id = et.tag_id WHERE et.entity_type = 'pool' AND et.entity_id = po.id AND t.name ILIKE '%' || $2 || '%')
		))

	UNION ALL
	SELECT 'reservation', r.id::text, COALESCE(r.hostname, host(r.reserved_ip)), host(r.reserved_ip), COALESCE(r.description, ''), '/reservations/' || r.id
	FROM dhcp_reservations r
	WHERE ($1::inet IS NOT NULL AND r.reserved_ip = $1)
		OR ($1::inet IS NULL AND $2::text IS NOT NULL AND (
			COALESCE(r.hostname, '') ILIKE '%' || $2 || '%' OR COALESCE(r.description, '') ILIKE '%' || $2 || '%'
			OR r.binding_key ILIKE '%' || $2 || '%'
			OR EXISTS (SELECT 1 FROM entity_tags et JOIN tags t ON t.id = et.tag_id WHERE et.entity_type = 'reservation' AND et.entity_id = r.id AND t.name ILIKE '%' || $2 || '%')
		))

	UNION ALL
	SELECT 'lease', l.id::text, host(l.ip_address), COALESCE(l.client_mac::text, ''), COALESCE(l.client_hostname, ''), '/leases/' || l.id
	FROM dhcp_leases l
	WHERE ($1::inet IS NOT NULL AND l.ip_address = $1)
		OR ($1::inet IS NULL AND $2::text IS NOT NULL AND (
			COALESCE(l.client_hostname, '') ILIKE '%' || $2 || '%' OR COALESCE(l.client_mac::text, '') ILIKE '%' || $2 || '%'
		))

	UNION ALL
	SELECT 'template', t.id::text, t.name, '', COALESCE(t.description, ''), '/templates/' || t.id
	FROM dhcp_templates t
	WHERE $1::inet IS NULL AND $2::text IS NOT NULL AND (t.name ILIKE '%' || $2 || '%' OR COALESCE(t.description, '') ILIKE '%' || $2 || '%')

	UNION ALL
	SELECT 'cluster', c.id::text, c.name, COALESCE(host(c.vip_address), ''), COALESCE(c.description, ''), '/clusters/' || c.id
	FROM clusters c
	WHERE $1::inet IS NULL AND $2::text IS NOT NULL AND (
		c.name ILIKE '%' || $2 || '%' OR COALESCE(c.description, '') ILIKE '%' || $2 || '%'
		OR EXISTS (SELECT 1 FROM entity_tags et JOIN tags t ON t.id = et.tag_id WHERE et.entity_type = 'cluster' AND et.entity_id = c.id AND t.name ILIKE '%' || $2 || '%')
	)

	-- Deliberately no 'server' or 'user' branches: GET /api/v1/search is
	-- viewer-accessible (router.go's reg.get requires only RoleViewer), but
	-- the user directory (username/email) is meant to be admin-only (see
	-- /users' explicit models.RoleAdmin gate in router.go) — a UNION branch
	-- here would bypass that gate for any authenticated viewer regardless
	-- of role. Server lookup (cluster_servers) is dropped alongside it for
	-- consistency, though it isn't itself a privilege escalation: the same
	-- fields are already viewer-accessible via GET
	-- /clusters/{cluster_id}/servers. Browse users via GET /users
	-- (admin-only) and servers via GET /clusters/{cluster_id}/servers
	-- instead of through this endpoint.
)
SELECT type, entity_id, title, subtitle, description, url
FROM matches
WHERE $3::text[] IS NULL OR type = ANY($3)
ORDER BY title
LIMIT $4`
