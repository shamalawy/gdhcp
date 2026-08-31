// postgres_config.go implements ConfigStore (the DHCP engine's hot-path
// scope/pool/option/template reads, including cached inheritance
// resolution), OptionCatalogStore, OptionValueStore, and TemplateStore.
package store

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/jackc/pgx/v5"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// ---------------------------------------------------------------------------
// VRF / Scope / Pool reads (ConfigStore)
// ---------------------------------------------------------------------------

const vrfSelectCols = `id, name, route_distinguisher, description, is_default, created_at, updated_at, created_by_user_id, updated_by_user_id`

func scanVRF(row pgx.Row) (*models.VRF, error) {
	var v models.VRF
	if err := row.Scan(&v.ID, &v.Name, &v.RouteDistinguisher, &v.Description, &v.IsDefault,
		&v.CreatedAt, &v.UpdatedAt, &v.CreatedByUserID, &v.UpdatedByUserID); err != nil {
		return nil, err
	}
	return &v, nil
}

func (p *Postgres) GetVRFByInterface(ctx context.Context, ifname string) (*models.VRF, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT `+prefixCols("v", vrfSelectCols)+`
		FROM server_interfaces si JOIN vrfs v ON v.id = si.vrf_id
		WHERE si.interface_name = $1
		LIMIT 1`, ifname)
	v, err := scanVRF(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return p.defaultVRF(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get vrf by interface %s: %w", ifname, err)
	}
	return v, nil
}

func (p *Postgres) defaultVRF(ctx context.Context) (*models.VRF, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+vrfSelectCols+` FROM vrfs WHERE is_default LIMIT 1`)
	v, err := scanVRF(row)
	if err != nil {
		return nil, mapReadError(err, "get default vrf")
	}
	return v, nil
}

func (p *Postgres) GetVRFByID(ctx context.Context, vrfID string) (*models.VRF, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+vrfSelectCols+` FROM vrfs WHERE id = $1`, vrfID)
	v, err := scanVRF(row)
	if err != nil {
		return nil, mapReadError(err, "get vrf "+vrfID)
	}
	return v, nil
}

const scopeSelectCols = `id, vrf_id, cluster_id, ipam_prefix_id, name, description, network, is_enabled,
	created_at, updated_at, created_by_user_id, updated_by_user_id`

func scanScope(row pgx.Row) (*models.DHCPScope, error) {
	var s models.DHCPScope
	var network net.IPNet
	if err := row.Scan(&s.ID, &s.VRFID, &s.ClusterID, &s.IPAMPrefixID, &s.Name, &s.Description, &network,
		&s.IsEnabled, &s.CreatedAt, &s.UpdatedAt, &s.CreatedByUserID, &s.UpdatedByUserID); err != nil {
		return nil, err
	}
	n := models.IPNet(network)
	s.Network = &n
	return &s, nil
}

func (p *Postgres) GetScopesByVRF(ctx context.Context, vrfID string) ([]*models.DHCPScope, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+scopeSelectCols+` FROM dhcp_scopes WHERE vrf_id = $1 ORDER BY name`, vrfID)
	if err != nil {
		return nil, fmt.Errorf("store: get scopes by vrf %s: %w", vrfID, err)
	}
	defer rows.Close()
	return collectScopes(rows)
}

func collectScopes(rows pgx.Rows) ([]*models.DHCPScope, error) {
	var out []*models.DHCPScope
	for rows.Next() {
		s, err := scanScope(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) GetScopeByID(ctx context.Context, scopeID string) (*models.DHCPScope, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+scopeSelectCols+` FROM dhcp_scopes WHERE id = $1`, scopeID)
	s, err := scanScope(row)
	if err != nil {
		return nil, mapReadError(err, "get scope "+scopeID)
	}
	return s, nil
}

// FindScopeByGiaddr finds the scope whose network contains giaddr (relay
// agent IP), for Option 82 routing.
func (p *Postgres) FindScopeByGiaddr(ctx context.Context, giaddr net.IP) (*models.DHCPScope, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+scopeSelectCols+` FROM dhcp_scopes
		WHERE network >> $1 OR network = $1::cidr
		ORDER BY masklen(network) DESC LIMIT 1`, giaddr.String())
	s, err := scanScope(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: find scope by giaddr %s: %w", giaddr, err)
	}
	return s, nil
}

func (p *Postgres) FindScopeByNetwork(ctx context.Context, vrfID string, network *net.IPNet) (*models.DHCPScope, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+scopeSelectCols+` FROM dhcp_scopes
		WHERE vrf_id = $1 AND network = $2::cidr`, vrfID, network.String())
	s, err := scanScope(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: find scope by network %s: %w", network, err)
	}
	return s, nil
}

const poolSelectCols = `id, scope_id, name, description, range_start, range_end, is_enabled, priority,
	created_at, updated_at, created_by_user_id, updated_by_user_id`

func scanPool(row pgx.Row) (*models.DHCPPool, error) {
	var pl models.DHCPPool
	if err := row.Scan(&pl.ID, &pl.ScopeID, &pl.Name, &pl.Description, &pl.RangeStart, &pl.RangeEnd, &pl.IsEnabled,
		&pl.Priority, &pl.CreatedAt, &pl.UpdatedAt, &pl.CreatedByUserID, &pl.UpdatedByUserID); err != nil {
		return nil, err
	}
	return &pl, nil
}

func (p *Postgres) GetPoolsByScope(ctx context.Context, scopeID string) ([]*models.DHCPPool, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+poolSelectCols+` FROM dhcp_pools WHERE scope_id = $1 ORDER BY priority DESC, name`, scopeID)
	if err != nil {
		return nil, fmt.Errorf("store: get pools by scope %s: %w", scopeID, err)
	}
	defer rows.Close()
	var out []*models.DHCPPool
	for rows.Next() {
		pl, err := scanPool(rows)
		if err != nil {
			return nil, fmt.Errorf("store: get pools by scope %s: %w", scopeID, err)
		}
		out = append(out, pl)
	}
	return out, rows.Err()
}

func (p *Postgres) GetPoolByID(ctx context.Context, poolID string) (*models.DHCPPool, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+poolSelectCols+` FROM dhcp_pools WHERE id = $1`, poolID)
	pl, err := scanPool(row)
	if err != nil {
		return nil, mapReadError(err, "get pool "+poolID)
	}
	return pl, nil
}

// CanAllocateInPool reports whether ip is inside poolID's range, not the
// scope's network/broadcast address, not covered by an enabled reservation
// (for a different client — reservation matching itself is handled by the
// caller), and not currently held by an active lease.
func (p *Postgres) CanAllocateInPool(ctx context.Context, poolID string, ip net.IP) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `
		SELECT
			$2::inet BETWEEN po.range_start AND po.range_end
			AND $2::inet <> set_masklen(network(sc.network::inet), 32)
			AND $2::inet <> set_masklen(broadcast(sc.network::inet), 32)
			AND NOT EXISTS (SELECT 1 FROM dhcp_leases l WHERE l.pool_id = $1 AND l.ip_address = $2 AND l.binding_state = 'active')
			AND NOT EXISTS (SELECT 1 FROM dhcp_reservations r WHERE r.pool_id = $1 AND r.reserved_ip = $2 AND r.is_enabled)
		FROM dhcp_pools po JOIN dhcp_scopes sc ON sc.id = po.scope_id
		WHERE po.id = $1`, poolID, ip).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: can allocate %s in pool %s: %w", ip, poolID, err)
	}
	return ok, nil
}

// ---------------------------------------------------------------------------
// Option values (reads live on ConfigStore per interface.go; writes on
// OptionValueStore below)
// ---------------------------------------------------------------------------

const optionValueSelectCols = `id, option_code, scope_level, scope_entity_id, value, is_enabled,
	created_at, updated_at, created_by_user_id, updated_by_user_id`

func scanOptionValue(row pgx.Row) (*models.DHCPOption, error) {
	var o models.DHCPOption
	if err := row.Scan(&o.ID, &o.OptionCode, &o.ScopeLevel, &o.ScopeEntityID, &o.Value, &o.IsEnabled,
		&o.CreatedAt, &o.UpdatedAt, &o.CreatedByUserID, &o.UpdatedByUserID); err != nil {
		return nil, err
	}
	return &o, nil
}

func (p *Postgres) optionsAtLevel(ctx context.Context, level models.OptionScopeLevel, entityID string) ([]*models.DHCPOption, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT `+prefixCols("ov", optionValueSelectCols)+`,
			c.option_code, c.name, c.value_type, c.description, c.iana_reference, c.created_at
		FROM dhcp_option_values ov
		JOIN dhcp_option_catalog c ON c.option_code = ov.option_code
		WHERE ov.scope_level = $1 AND ov.scope_entity_id = $2
		ORDER BY ov.option_code`, level, entityID)
	if err != nil {
		return nil, fmt.Errorf("store: get options at %s %s: %w", level, entityID, err)
	}
	defer rows.Close()

	// Initialized (not nil) so a zero-row result marshals to `[]`, not
	// `null` — the frontend (effective-options.component.ts) iterates this
	// unconditionally and throws on a null value.
	out := []*models.DHCPOption{}
	for rows.Next() {
		o, cat, err := scanOptionValueWithCatalog(rows)
		if err != nil {
			return nil, fmt.Errorf("store: get options at %s %s: %w", level, entityID, err)
		}
		o.Catalog = cat
		out = append(out, o)
	}
	return out, rows.Err()
}

func scanOptionValueWithCatalog(row pgx.Row) (*models.DHCPOption, *models.OptionCatalogEntry, error) {
	var o models.DHCPOption
	var cat models.OptionCatalogEntry
	if err := row.Scan(&o.ID, &o.OptionCode, &o.ScopeLevel, &o.ScopeEntityID, &o.Value, &o.IsEnabled,
		&o.CreatedAt, &o.UpdatedAt, &o.CreatedByUserID, &o.UpdatedByUserID,
		&cat.OptionCode, &cat.Name, &cat.ValueType, &cat.Description, &cat.IANAReference, &cat.CreatedAt,
	); err != nil {
		return nil, nil, err
	}
	return &o, &cat, nil
}

func (p *Postgres) GetOptionsByScope(ctx context.Context, scopeID string) ([]*models.DHCPOption, error) {
	return p.optionsAtLevel(ctx, models.OptionScopeScope, scopeID)
}

func (p *Postgres) GetOptionsByPool(ctx context.Context, poolID string) ([]*models.DHCPOption, error) {
	return p.optionsAtLevel(ctx, models.OptionScopePool, poolID)
}

func (p *Postgres) GetOptionsByReservation(ctx context.Context, reservationID string) ([]*models.DHCPOption, error) {
	return p.optionsAtLevel(ctx, models.OptionScopeReservation, reservationID)
}

func (p *Postgres) GetOptionsByTemplate(ctx context.Context, templateID string) ([]*models.DHCPOption, error) {
	return p.optionsAtLevel(ctx, models.OptionScopeTemplate, templateID)
}

func (p *Postgres) GetGlobalOptions(ctx context.Context) ([]*models.DHCPOption, error) {
	return p.optionsAtLevel(ctx, models.OptionScopeGlobal, globalScopeEntityID)
}

// optionValueAt returns the single enabled option value at (code, level,
// entityID), or nil if none is set / it's disabled.
func (p *Postgres) optionValueAt(ctx context.Context, code int16, level models.OptionScopeLevel, entityID string) (*models.DHCPOption, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT `+prefixCols("ov", optionValueSelectCols)+`,
			c.option_code, c.name, c.value_type, c.description, c.iana_reference, c.created_at
		FROM dhcp_option_values ov
		JOIN dhcp_option_catalog c ON c.option_code = ov.option_code
		WHERE ov.option_code = $1 AND ov.scope_level = $2 AND ov.scope_entity_id = $3 AND ov.is_enabled`,
		code, level, entityID)
	o, cat, err := scanOptionValueWithCatalog(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: resolve option %d at %s %s: %w", code, level, entityID, err)
	}
	o.Catalog = cat
	return o, nil
}

// orderedTemplateIDs returns the IDs of templates associated with scopeID
// and/or poolID, ordered by priority descending (highest priority checked
// first), scope associations before pool associations at equal priority.
// An absent scope/pool is passed as a NULL uuid rather than an empty string:
// `scope_id = $1 AND $1 <> ”` reads naturally but Postgres infers $1's type
// as uuid from the equality, then casts the ” literal to uuid too, so the
// statement fails with `invalid input syntax for type uuid: ""` on *every*
// call, whatever the arguments. `scope_id = NULL` is never true, which is the
// skip this guard was reaching for.
func (p *Postgres) orderedTemplateIDs(ctx context.Context, scopeID, poolID string) ([]string, error) {
	if scopeID == "" && poolID == "" {
		return nil, nil
	}
	var scopeArg, poolArg any
	if scopeID != "" {
		scopeArg = scopeID
	}
	if poolID != "" {
		poolArg = poolID
	}
	rows, err := p.pool.Query(ctx, `
		SELECT template_id, priority, 0 AS src FROM dhcp_template_scopes WHERE scope_id = $1::uuid
		UNION ALL
		SELECT template_id, priority, 1 AS src FROM dhcp_template_pools WHERE pool_id = $2::uuid
		ORDER BY priority DESC, src ASC`, scopeArg, poolArg)
	if err != nil {
		return nil, fmt.Errorf("store: ordered templates for scope %s pool %s: %w", scopeID, poolID, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		var priority, src int
		if err := rows.Scan(&id, &priority, &src); err != nil {
			return nil, fmt.Errorf("store: ordered templates for scope %s pool %s: %w", scopeID, poolID, err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ResolveOption walks the inheritance chain reservation > pool > scope >
// template (priority) > global, caching the result for
// config.DatabaseConfig.OptionCacheTTL.
func (p *Postgres) ResolveOption(ctx context.Context, optCode int16, scopeID, poolID, reservationID string) (*models.DHCPOption, error) {
	key := optionCacheKey{code: optCode, scopeID: scopeID, poolID: poolID, reservationID: reservationID}
	if v, hit := p.cache.get(key); hit {
		return v, nil
	}

	if reservationID != "" {
		if v, err := p.optionValueAt(ctx, optCode, models.OptionScopeReservation, reservationID); err != nil {
			return nil, err
		} else if v != nil {
			p.cache.set(key, v)
			return v, nil
		}
	}
	if poolID != "" {
		if v, err := p.optionValueAt(ctx, optCode, models.OptionScopePool, poolID); err != nil {
			return nil, err
		} else if v != nil {
			p.cache.set(key, v)
			return v, nil
		}
	}
	if scopeID != "" {
		if v, err := p.optionValueAt(ctx, optCode, models.OptionScopeScope, scopeID); err != nil {
			return nil, err
		} else if v != nil {
			p.cache.set(key, v)
			return v, nil
		}
	}

	templateIDs, err := p.orderedTemplateIDs(ctx, scopeID, poolID)
	if err != nil {
		return nil, err
	}
	for _, tid := range templateIDs {
		if v, err := p.optionValueAt(ctx, optCode, models.OptionScopeTemplate, tid); err != nil {
			return nil, err
		} else if v != nil {
			p.cache.set(key, v)
			return v, nil
		}
	}

	v, err := p.optionValueAt(ctx, optCode, models.OptionScopeGlobal, globalScopeEntityID)
	if err != nil {
		return nil, err
	}
	p.cache.set(key, v)
	return v, nil
}

// ---------------------------------------------------------------------------
// Templates (ConfigStore reads + TemplateStore CRUD/associations)
// ---------------------------------------------------------------------------

const templateSelectCols = `id, name, description, is_enabled, created_at, updated_at, created_by_user_id, updated_by_user_id`

func scanTemplate(row pgx.Row) (*models.Template, error) {
	var t models.Template
	if err := row.Scan(&t.ID, &t.Name, &t.Description, &t.IsEnabled, &t.CreatedAt, &t.UpdatedAt,
		&t.CreatedByUserID, &t.UpdatedByUserID); err != nil {
		return nil, err
	}
	return &t, nil
}

func (p *Postgres) GetTemplateByID(ctx context.Context, templateID string) (*models.Template, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+templateSelectCols+` FROM dhcp_templates WHERE id = $1`, templateID)
	t, err := scanTemplate(row)
	if err != nil {
		return nil, mapReadError(err, "get template "+templateID)
	}
	return t, nil
}

func (p *Postgres) GetTemplatesByScope(ctx context.Context, scopeID string) ([]*models.Template, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT `+prefixCols("t", templateSelectCols)+`
		FROM dhcp_template_scopes ts JOIN dhcp_templates t ON t.id = ts.template_id
		WHERE ts.scope_id = $1
		ORDER BY ts.priority DESC, t.name`, scopeID)
	if err != nil {
		return nil, fmt.Errorf("store: get templates by scope %s: %w", scopeID, err)
	}
	defer rows.Close()

	var out []*models.Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("store: get templates by scope %s: %w", scopeID, err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Postgres) ListTemplates(ctx context.Context, params ListParams) (ListResult[*models.Template], error) {
	limit, offset := normalizeListParams(params)
	search := nilIfEmpty(params.Search)

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_templates WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')`, search).Scan(&total); err != nil {
		return ListResult[*models.Template]{}, fmt.Errorf("store: count templates: %w", err)
	}

	order := orderByClause(params.Sort, map[string]string{"name": "name", "created_at": "created_at"}, "name ASC")
	rows, err := p.pool.Query(ctx, `SELECT `+templateSelectCols+` FROM dhcp_templates
		WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')
		ORDER BY `+order+` LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return ListResult[*models.Template]{}, fmt.Errorf("store: list templates: %w", err)
	}
	defer rows.Close()

	var items []*models.Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return ListResult[*models.Template]{}, fmt.Errorf("store: list templates: %w", err)
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.Template]{}, fmt.Errorf("store: list templates: %w", err)
	}
	return ListResult[*models.Template]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) CreateTemplate(ctx context.Context, t *models.Template) error {
	if t.ID == "" {
		t.ID = newUUID()
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO dhcp_templates (id, name, description, is_enabled, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $5)
		RETURNING created_at, updated_at`,
		t.ID, t.Name, t.Description, t.IsEnabled, t.CreatedByUserID,
	).Scan(&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create template")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) UpdateTemplate(ctx context.Context, t *models.Template) error {
	err := p.pool.QueryRow(ctx, `
		UPDATE dhcp_templates SET name=$2, description=$3, is_enabled=$4, updated_by_user_id=$5, updated_at=now()
		WHERE id = $1 RETURNING updated_at`,
		t.ID, t.Name, t.Description, t.IsEnabled, t.UpdatedByUserID,
	).Scan(&t.UpdatedAt)
	if err != nil {
		return mapWriteError(mapReadError(err, "update template "+t.ID), "update template")
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) DeleteTemplate(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM dhcp_templates WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete template")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete template %s: %w", id, models.ErrNotFound)
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) ListTemplateScopeAssocs(ctx context.Context, templateID string) ([]*models.TemplateScopeAssoc, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, template_id, scope_id, priority, created_at
		FROM dhcp_template_scopes WHERE template_id = $1 ORDER BY priority DESC`, templateID)
	if err != nil {
		return nil, fmt.Errorf("store: list template scope assocs %s: %w", templateID, err)
	}
	defer rows.Close()
	var out []*models.TemplateScopeAssoc
	for rows.Next() {
		var a models.TemplateScopeAssoc
		if err := rows.Scan(&a.ID, &a.TemplateID, &a.ScopeID, &a.Priority, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: list template scope assocs %s: %w", templateID, err)
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

func (p *Postgres) AssociateTemplateScope(ctx context.Context, templateID, scopeID string, priority int) (*models.TemplateScopeAssoc, error) {
	a := &models.TemplateScopeAssoc{ID: newUUID(), TemplateID: templateID, ScopeID: scopeID, Priority: priority}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO dhcp_template_scopes (id, template_id, scope_id, priority) VALUES ($1, $2, $3, $4)
		ON CONFLICT (template_id, scope_id) DO UPDATE SET priority = EXCLUDED.priority
		RETURNING created_at`, a.ID, templateID, scopeID, priority).Scan(&a.CreatedAt)
	if err != nil {
		return nil, mapWriteError(err, "associate template scope")
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return a, nil
}

func (p *Postgres) DisassociateTemplateScope(ctx context.Context, templateID, scopeID string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM dhcp_template_scopes WHERE template_id = $1 AND scope_id = $2`, templateID, scopeID)
	if err != nil {
		return mapWriteError(err, "disassociate template scope")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: disassociate template %s scope %s: %w", templateID, scopeID, models.ErrNotFound)
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) ListTemplatePoolAssocs(ctx context.Context, templateID string) ([]*models.TemplatePoolAssoc, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, template_id, pool_id, priority, created_at
		FROM dhcp_template_pools WHERE template_id = $1 ORDER BY priority DESC`, templateID)
	if err != nil {
		return nil, fmt.Errorf("store: list template pool assocs %s: %w", templateID, err)
	}
	defer rows.Close()
	var out []*models.TemplatePoolAssoc
	for rows.Next() {
		var a models.TemplatePoolAssoc
		if err := rows.Scan(&a.ID, &a.TemplateID, &a.PoolID, &a.Priority, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: list template pool assocs %s: %w", templateID, err)
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

func (p *Postgres) AssociateTemplatePool(ctx context.Context, templateID, poolID string, priority int) (*models.TemplatePoolAssoc, error) {
	a := &models.TemplatePoolAssoc{ID: newUUID(), TemplateID: templateID, PoolID: poolID, Priority: priority}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO dhcp_template_pools (id, template_id, pool_id, priority) VALUES ($1, $2, $3, $4)
		ON CONFLICT (template_id, pool_id) DO UPDATE SET priority = EXCLUDED.priority
		RETURNING created_at`, a.ID, templateID, poolID, priority).Scan(&a.CreatedAt)
	if err != nil {
		return nil, mapWriteError(err, "associate template pool")
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return a, nil
}

func (p *Postgres) DisassociateTemplatePool(ctx context.Context, templateID, poolID string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM dhcp_template_pools WHERE template_id = $1 AND pool_id = $2`, templateID, poolID)
	if err != nil {
		return mapWriteError(err, "disassociate template pool")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: disassociate template %s pool %s: %w", templateID, poolID, models.ErrNotFound)
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return nil
}

// ---------------------------------------------------------------------------
// OptionCatalogStore
// ---------------------------------------------------------------------------

const catalogSelectCols = `option_code, name, value_type, description, iana_reference, created_at`

func scanCatalogEntry(row pgx.Row) (*models.OptionCatalogEntry, error) {
	var c models.OptionCatalogEntry
	if err := row.Scan(&c.OptionCode, &c.Name, &c.ValueType, &c.Description, &c.IANAReference, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (p *Postgres) ListOptionCatalog(ctx context.Context, params ListParams) (ListResult[*models.OptionCatalogEntry], error) {
	limit, offset := normalizeListParams(params)
	var standardArg any
	switch params.Filters["standard"] {
	case "true":
		standardArg = true
	case "false":
		standardArg = false
	}
	switch params.Filters["custom"] {
	case "true":
		standardArg = false
	case "false":
		standardArg = true
	}

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM dhcp_option_catalog
		WHERE $1::bool IS NULL OR (iana_reference IS NOT NULL) = $1`, standardArg).Scan(&total); err != nil {
		return ListResult[*models.OptionCatalogEntry]{}, fmt.Errorf("store: count option catalog: %w", err)
	}

	rows, err := p.pool.Query(ctx, `SELECT `+catalogSelectCols+` FROM dhcp_option_catalog
		WHERE $1::bool IS NULL OR (iana_reference IS NOT NULL) = $1
		ORDER BY option_code LIMIT $2 OFFSET $3`, standardArg, limit, offset)
	if err != nil {
		return ListResult[*models.OptionCatalogEntry]{}, fmt.Errorf("store: list option catalog: %w", err)
	}
	defer rows.Close()

	var items []*models.OptionCatalogEntry
	for rows.Next() {
		c, err := scanCatalogEntry(rows)
		if err != nil {
			return ListResult[*models.OptionCatalogEntry]{}, fmt.Errorf("store: list option catalog: %w", err)
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.OptionCatalogEntry]{}, fmt.Errorf("store: list option catalog: %w", err)
	}
	return ListResult[*models.OptionCatalogEntry]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) GetAllOptionCatalog(ctx context.Context) ([]*models.OptionCatalogEntry, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+catalogSelectCols+` FROM dhcp_option_catalog ORDER BY option_code`)
	if err != nil {
		return nil, fmt.Errorf("store: get all option catalog: %w", err)
	}
	defer rows.Close()
	var items []*models.OptionCatalogEntry
	for rows.Next() {
		c, err := scanCatalogEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("store: get all option catalog: %w", err)
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func (p *Postgres) GetOptionCatalogEntry(ctx context.Context, code int16) (*models.OptionCatalogEntry, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+catalogSelectCols+` FROM dhcp_option_catalog WHERE option_code = $1`, code)
	c, err := scanCatalogEntry(row)
	if err != nil {
		return nil, mapReadError(err, fmt.Sprintf("get option catalog entry %d", code))
	}
	return c, nil
}

func (p *Postgres) CreateOptionCatalogEntry(ctx context.Context, entry *models.OptionCatalogEntry) error {
	if !entry.ValueType.Valid() {
		return fmt.Errorf("store: create option catalog entry: invalid value_type %q: %w", entry.ValueType, models.ErrInvalidInput)
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO dhcp_option_catalog (option_code, name, value_type, description, iana_reference)
		VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		entry.OptionCode, entry.Name, entry.ValueType, entry.Description, entry.IANAReference,
	).Scan(&entry.CreatedAt)
	if err != nil {
		return mapWriteError(err, "create option catalog entry")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) DeleteOptionCatalogEntry(ctx context.Context, code int16) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM dhcp_option_catalog WHERE option_code = $1`, code)
	if err != nil {
		return mapWriteError(err, "delete option catalog entry")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete option catalog entry %d: %w", code, models.ErrNotFound)
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return nil
}

// ---------------------------------------------------------------------------
// OptionValueStore
// ---------------------------------------------------------------------------

func (p *Postgres) CreateOptionValue(ctx context.Context, ov *models.DHCPOption) error {
	if !ov.ScopeLevel.Valid() {
		return fmt.Errorf("store: create option value: invalid scope_level %q: %w", ov.ScopeLevel, models.ErrInvalidInput)
	}
	if ov.ID == "" {
		ov.ID = newUUID()
	}
	if ov.ScopeLevel == models.OptionScopeGlobal {
		ov.ScopeEntityID = globalScopeEntityID
	}

	err := p.pool.QueryRow(ctx, `
		INSERT INTO dhcp_option_values (id, option_code, scope_level, scope_entity_id, value, is_enabled, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING created_at, updated_at`,
		ov.ID, ov.OptionCode, ov.ScopeLevel, ov.ScopeEntityID, ov.Value, ov.IsEnabled, ov.CreatedByUserID,
	).Scan(&ov.CreatedAt, &ov.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create option value")
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) UpdateOptionValue(ctx context.Context, id string, value string, isEnabled bool) (*models.DHCPOption, error) {
	row := p.pool.QueryRow(ctx, `
		UPDATE dhcp_option_values SET value = $2, is_enabled = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+optionValueSelectCols, id, value, isEnabled)
	ov, err := scanOptionValue(row)
	if err != nil {
		return nil, mapReadError(err, "update option value "+id)
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return ov, nil
}

func (p *Postgres) DeleteOptionValue(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM dhcp_option_values WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete option value")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete option value %s: %w", id, models.ErrNotFound)
	}
	p.cache.invalidateAll()
	p.notifyConfigChanged(ctx)
	return nil
}
