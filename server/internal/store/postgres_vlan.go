// postgres_vlan.go implements VLANStore: CRUD for VLAN groups and VLANs,
// plus the prefix-assignment reads the IPAM tree needs.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

const vlanGroupSelectCols = `id, name, description, vrf_id, min_vid, max_vid,
	created_at, updated_at, created_by_user_id, updated_by_user_id`

func scanVLANGroup(row pgx.Row) (*models.VLANGroup, error) {
	var g models.VLANGroup
	if err := row.Scan(&g.ID, &g.Name, &g.Description, &g.VRFID, &g.MinVID, &g.MaxVID,
		&g.CreatedAt, &g.UpdatedAt, &g.CreatedByUserID, &g.UpdatedByUserID); err != nil {
		return nil, err
	}
	return &g, nil
}

// vlanSelectCols joins the group name and the number of prefixes assigned to
// each VLAN, so a list row is renderable without follow-up queries.
const vlanSelectCols = `v.id, v.group_id, v.vid, v.name, v.description, v.status,
	g.name AS group_name,
	(SELECT count(*) FROM ipam_prefixes ip WHERE ip.vlan_id = v.id) AS prefix_count,
	v.created_at, v.updated_at, v.created_by_user_id, v.updated_by_user_id`

const vlanFrom = ` FROM vlans v LEFT JOIN vlan_groups g ON g.id = v.group_id`

func scanVLAN(row pgx.Row) (*models.VLAN, error) {
	var v models.VLAN
	if err := row.Scan(&v.ID, &v.GroupID, &v.VID, &v.Name, &v.Description, &v.Status,
		&v.GroupName, &v.PrefixCount,
		&v.CreatedAt, &v.UpdatedAt, &v.CreatedByUserID, &v.UpdatedByUserID); err != nil {
		return nil, err
	}
	return &v, nil
}

// ---------------------------------------------------------------------------
// VLAN groups
// ---------------------------------------------------------------------------

func (p *Postgres) ListVLANGroups(ctx context.Context, params ListParams) (ListResult[*models.VLANGroup], error) {
	limit, offset := normalizeListParams(params)
	search := nilIfEmpty(params.Search)

	var total int64
	if err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM vlan_groups WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')`,
		search).Scan(&total); err != nil {
		return ListResult[*models.VLANGroup]{}, fmt.Errorf("store: count vlan groups: %w", err)
	}

	order := orderByClause(params.Sort, map[string]string{"name": "name", "created_at": "created_at"}, "name ASC")
	rows, err := p.pool.Query(ctx, `SELECT `+vlanGroupSelectCols+` FROM vlan_groups
		WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')
		ORDER BY `+order+` LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return ListResult[*models.VLANGroup]{}, fmt.Errorf("store: list vlan groups: %w", err)
	}
	defer rows.Close()

	items := make([]*models.VLANGroup, 0)
	for rows.Next() {
		g, err := scanVLANGroup(rows)
		if err != nil {
			return ListResult[*models.VLANGroup]{}, fmt.Errorf("store: list vlan groups: %w", err)
		}
		items = append(items, g)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.VLANGroup]{}, fmt.Errorf("store: list vlan groups: %w", err)
	}
	return ListResult[*models.VLANGroup]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) GetVLANGroupByID(ctx context.Context, id string) (*models.VLANGroup, error) {
	g, err := scanVLANGroup(p.pool.QueryRow(ctx, `SELECT `+vlanGroupSelectCols+` FROM vlan_groups WHERE id = $1`, id))
	if err != nil {
		return nil, mapReadError(err, "get vlan group")
	}
	return g, nil
}

func (p *Postgres) CreateVLANGroup(ctx context.Context, g *models.VLANGroup) error {
	if g.ID == "" {
		g.ID = newUUID()
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO vlan_groups (id, name, description, vrf_id, min_vid, max_vid, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING created_at, updated_at`,
		g.ID, g.Name, g.Description, g.VRFID, g.MinVID, g.MaxVID, g.CreatedByUserID,
	).Scan(&g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create vlan group")
	}
	return nil
}

func (p *Postgres) UpdateVLANGroup(ctx context.Context, g *models.VLANGroup) error {
	err := p.pool.QueryRow(ctx, `
		UPDATE vlan_groups SET name = $2, description = $3, vrf_id = $4, min_vid = $5, max_vid = $6,
			updated_at = now(), updated_by_user_id = $7
		WHERE id = $1 RETURNING updated_at`,
		g.ID, g.Name, g.Description, g.VRFID, g.MinVID, g.MaxVID, g.UpdatedByUserID,
	).Scan(&g.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "update vlan group")
	}
	return nil
}

func (p *Postgres) DeleteVLANGroup(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM vlan_groups WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete vlan group")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete vlan group %s: %w", id, models.ErrNotFound)
	}
	return nil
}

// ---------------------------------------------------------------------------
// VLANs
// ---------------------------------------------------------------------------

// ListVLANs supports filters "group_id" (the literal "none" selects
// ungrouped VLANs) and "status", plus free-text search over name and VID.
func (p *Postgres) ListVLANs(ctx context.Context, params ListParams) (ListResult[*models.VLAN], error) {
	limit, offset := normalizeListParams(params)
	search := nilIfEmpty(params.Search)

	// "none" is the sentinel for "ungrouped", since a NULL group_id can't be
	// expressed as a query-string value.
	ungrouped := params.Filters["group_id"] == "none"
	groupID := nilIfEmpty(params.Filters["group_id"])
	if ungrouped {
		groupID = nil
	}
	status := nilIfEmpty(params.Filters["status"])

	where := `WHERE ($1::uuid IS NULL OR v.group_id = $1)
		AND ($2::bool IS FALSE OR v.group_id IS NULL)
		AND ($3::text IS NULL OR v.status = $3)
		AND ($4::text IS NULL OR v.name ILIKE '%'||$4||'%' OR v.vid::text = $4)`
	args := []any{groupID, ungrouped, status, search}

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*)`+vlanFrom+` `+where, args...).Scan(&total); err != nil {
		return ListResult[*models.VLAN]{}, fmt.Errorf("store: count vlans: %w", err)
	}

	order := orderByClause(params.Sort, map[string]string{
		"vid": "v.vid", "name": "v.name", "created_at": "v.created_at",
	}, "v.vid ASC")
	rows, err := p.pool.Query(ctx, `SELECT `+vlanSelectCols+vlanFrom+` `+where+
		` ORDER BY `+order+` LIMIT $5 OFFSET $6`, append(args, limit, offset)...)
	if err != nil {
		return ListResult[*models.VLAN]{}, fmt.Errorf("store: list vlans: %w", err)
	}
	defer rows.Close()

	items := make([]*models.VLAN, 0)
	for rows.Next() {
		v, err := scanVLAN(rows)
		if err != nil {
			return ListResult[*models.VLAN]{}, fmt.Errorf("store: list vlans: %w", err)
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.VLAN]{}, fmt.Errorf("store: list vlans: %w", err)
	}
	return ListResult[*models.VLAN]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) GetVLANByID(ctx context.Context, id string) (*models.VLAN, error) {
	v, err := scanVLAN(p.pool.QueryRow(ctx, `SELECT `+vlanSelectCols+vlanFrom+` WHERE v.id = $1`, id))
	if err != nil {
		return nil, mapReadError(err, "get vlan")
	}
	return v, nil
}

func (p *Postgres) CreateVLAN(ctx context.Context, v *models.VLAN) error {
	if v.ID == "" {
		v.ID = newUUID()
	}
	if v.Status == "" {
		v.Status = models.VLANStatusActive
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO vlans (id, group_id, vid, name, description, status, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING created_at, updated_at`,
		v.ID, v.GroupID, v.VID, v.Name, v.Description, v.Status, v.CreatedByUserID,
	).Scan(&v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create vlan")
	}
	return nil
}

func (p *Postgres) UpdateVLAN(ctx context.Context, v *models.VLAN) error {
	err := p.pool.QueryRow(ctx, `
		UPDATE vlans SET group_id = $2, vid = $3, name = $4, description = $5, status = $6,
			updated_at = now(), updated_by_user_id = $7
		WHERE id = $1 RETURNING updated_at`,
		v.ID, v.GroupID, v.VID, v.Name, v.Description, v.Status, v.UpdatedByUserID,
	).Scan(&v.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "update vlan")
	}
	return nil
}

// DeleteVLAN removes a VLAN. Prefixes referencing it are unassigned by the
// ON DELETE SET NULL on ipam_prefixes.vlan_id rather than deleted.
func (p *Postgres) DeleteVLAN(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM vlans WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete vlan")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete vlan %s: %w", id, models.ErrNotFound)
	}
	p.notifyConfigChanged(ctx)
	return nil
}

// ListPrefixesByVLAN returns every prefix assigned to a VLAN, for the VLAN
// detail page's "prefixes on this VLAN" table.
func (p *Postgres) ListPrefixesByVLAN(ctx context.Context, vlanID string) ([]*models.IPAMPrefix, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+prefixSelectCols+prefixFrom+`
		WHERE p.vlan_id = $1 ORDER BY p.prefix`, vlanID)
	if err != nil {
		return nil, fmt.Errorf("store: list prefixes by vlan %s: %w", vlanID, err)
	}
	defer rows.Close()

	items := make([]*models.IPAMPrefix, 0)
	for rows.Next() {
		pfx, err := scanPrefix(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list prefixes by vlan %s: %w", vlanID, err)
		}
		items = append(items, pfx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list prefixes by vlan %s: %w", vlanID, err)
	}
	return items, nil
}
