// postgres_ipam.go implements IPAMStore (hierarchy reads), IPAMAdminStore
// (CRUD, tree assembly), VRFStore, and AllocationHelperStore.NextAvailableSubnet.
package store

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// prefixSelectCols is qualified with the "p" alias and paired with
// prefixFrom, so every prefix read carries its VLAN's VID and name without a
// second query (the tree and prefix tables both render a VLAN chip).
const prefixSelectCols = `p.id, p.vrf_id, p.prefix, p.parent_id, p.prefix_type, p.name, p.description,
	p.utilization_percentage, p.vlan_id, vl.vid, vl.name,
	p.created_at, p.updated_at, p.created_by_user_id, p.updated_by_user_id`

const prefixFrom = ` FROM ipam_prefixes p LEFT JOIN vlans vl ON vl.id = p.vlan_id`

func scanPrefix(row pgx.Row) (*models.IPAMPrefix, error) {
	var pfx models.IPAMPrefix
	var ipnet net.IPNet
	if err := row.Scan(&pfx.ID, &pfx.VRFID, &ipnet, &pfx.ParentID, &pfx.PrefixType, &pfx.Name, &pfx.Description,
		&pfx.UtilizationPercentage, &pfx.VLANID, &pfx.VLANVID, &pfx.VLANName,
		&pfx.CreatedAt, &pfx.UpdatedAt, &pfx.CreatedByUserID, &pfx.UpdatedByUserID); err != nil {
		return nil, err
	}
	n := models.IPNet(ipnet)
	pfx.Prefix = &n
	return &pfx, nil
}

func (p *Postgres) GetPrefixByID(ctx context.Context, prefixID string) (*models.IPAMPrefix, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+prefixSelectCols+prefixFrom+` WHERE p.id = $1`, prefixID)
	pfx, err := scanPrefix(row)
	if err != nil {
		return nil, mapReadError(err, "get prefix "+prefixID)
	}
	return pfx, nil
}

func collectPrefixes(rows pgx.Rows) ([]*models.IPAMPrefix, error) {
	var out []*models.IPAMPrefix
	for rows.Next() {
		pfx, err := scanPrefix(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pfx)
	}
	return out, rows.Err()
}

func (p *Postgres) GetPrefixesByVRF(ctx context.Context, vrfID string) ([]*models.IPAMPrefix, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+prefixSelectCols+prefixFrom+`
		WHERE p.vrf_id = $1 AND p.parent_id IS NULL ORDER BY p.prefix`, vrfID)
	if err != nil {
		return nil, fmt.Errorf("store: get prefixes by vrf %s: %w", vrfID, err)
	}
	defer rows.Close()
	out, err := collectPrefixes(rows)
	if err != nil {
		return nil, fmt.Errorf("store: get prefixes by vrf %s: %w", vrfID, err)
	}
	return out, nil
}

func (p *Postgres) GetChildPrefixes(ctx context.Context, parentID string) ([]*models.IPAMPrefix, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+prefixSelectCols+prefixFrom+`
		WHERE p.parent_id = $1 ORDER BY p.prefix`, parentID)
	if err != nil {
		return nil, fmt.Errorf("store: get child prefixes of %s: %w", parentID, err)
	}
	defer rows.Close()
	out, err := collectPrefixes(rows)
	if err != nil {
		return nil, fmt.Errorf("store: get child prefixes of %s: %w", parentID, err)
	}
	return out, nil
}

// GetAncestorPrefixes returns ancestors of prefixID, nearest first, via a
// recursive walk up parent_id.
func (p *Postgres) GetAncestorPrefixes(ctx context.Context, prefixID string) ([]*models.IPAMPrefix, error) {
	rows, err := p.pool.Query(ctx, `
		WITH RECURSIVE ancestors AS (
			SELECT id, vrf_id, prefix, parent_id, prefix_type, name, description, utilization_percentage, vlan_id,
				created_at, updated_at, created_by_user_id, updated_by_user_id, 0 AS depth
			FROM ipam_prefixes WHERE id = (SELECT parent_id FROM ipam_prefixes WHERE id = $1)
			UNION ALL
			SELECT pp.id, pp.vrf_id, pp.prefix, pp.parent_id, pp.prefix_type, pp.name, pp.description,
				pp.utilization_percentage, pp.vlan_id,
				pp.created_at, pp.updated_at, pp.created_by_user_id, pp.updated_by_user_id, a.depth + 1
			FROM ipam_prefixes pp JOIN ancestors a ON pp.id = a.parent_id
		)
		SELECT p.id, p.vrf_id, p.prefix, p.parent_id, p.prefix_type, p.name, p.description,
			p.utilization_percentage, p.vlan_id, vl.vid, vl.name,
			p.created_at, p.updated_at, p.created_by_user_id, p.updated_by_user_id
		FROM ancestors p LEFT JOIN vlans vl ON vl.id = p.vlan_id
		-- Root first, so the result reads as a path: it drives both the
		-- breadcrumb trail and the tree's expand-to-reveal.
		ORDER BY p.depth DESC`, prefixID)
	if err != nil {
		return nil, fmt.Errorf("store: get ancestor prefixes of %s: %w", prefixID, err)
	}
	defer rows.Close()
	out, err := collectPrefixes(rows)
	if err != nil {
		return nil, fmt.Errorf("store: get ancestor prefixes of %s: %w", prefixID, err)
	}
	return out, nil
}

// FindPrefixContaining returns the most specific (masklen-ordered) existing
// prefix in vrfID that strictly contains cidr, or nil if none does.
func (p *Postgres) FindPrefixContaining(ctx context.Context, vrfID string, cidr *net.IPNet) (*models.IPAMPrefix, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+prefixSelectCols+prefixFrom+`
		WHERE p.vrf_id = $1 AND p.prefix >> $2::cidr
		ORDER BY masklen(p.prefix) DESC LIMIT 1`, vrfID, cidr.String())
	pfx, err := scanPrefix(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: find prefix containing %s in vrf %s: %w", cidr, vrfID, err)
	}
	return pfx, nil
}

// ListPrefixesInCIDR returns all prefixes (any level) contained within or
// equal to cidr.
func (p *Postgres) ListPrefixesInCIDR(ctx context.Context, vrfID string, cidr *net.IPNet) ([]*models.IPAMPrefix, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+prefixSelectCols+prefixFrom+`
		WHERE p.vrf_id = $1 AND p.prefix <<= $2::cidr ORDER BY p.prefix`, vrfID, cidr.String())
	if err != nil {
		return nil, fmt.Errorf("store: list prefixes in cidr %s: %w", cidr, err)
	}
	defer rows.Close()
	out, err := collectPrefixes(rows)
	if err != nil {
		return nil, fmt.Errorf("store: list prefixes in cidr %s: %w", cidr, err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// IPAMAdminStore
// ---------------------------------------------------------------------------

func (p *Postgres) ListPrefixes(ctx context.Context, params ListParams) (ListResult[*models.IPAMPrefix], error) {
	limit, offset := normalizeListParams(params)
	vrfID := nilIfEmpty(params.Filters["vrf_id"])
	ptype := nilIfEmpty(params.Filters["type"])
	// parent_id: the literal "root" means "no parent" (parent_id IS NULL);
	// any other value is an exact parent_id match. Omitted entirely means
	// "don't filter by parent" (used by the flat/unscoped prefix list).
	parentID := nilIfEmpty(params.Filters["parent_id"])

	const where = `WHERE ($1::uuid IS NULL OR p.vrf_id = $1) AND ($2::text IS NULL OR p.prefix_type = $2)
		AND ($3::text IS NULL OR (CASE WHEN $3 = 'root' THEN p.parent_id IS NULL ELSE p.parent_id = $3::uuid END))`

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*)`+prefixFrom+` `+where, vrfID, ptype, parentID).Scan(&total); err != nil {
		return ListResult[*models.IPAMPrefix]{}, fmt.Errorf("store: count prefixes: %w", err)
	}

	rows, err := p.pool.Query(ctx, `SELECT `+prefixSelectCols+prefixFrom+` `+where+
		` ORDER BY p.prefix LIMIT $4 OFFSET $5`, vrfID, ptype, parentID, limit, offset)
	if err != nil {
		return ListResult[*models.IPAMPrefix]{}, fmt.Errorf("store: list prefixes: %w", err)
	}
	defer rows.Close()
	items, err := collectPrefixes(rows)
	if err != nil {
		return ListResult[*models.IPAMPrefix]{}, fmt.Errorf("store: list prefixes: %w", err)
	}
	return ListResult[*models.IPAMPrefix]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) CreatePrefix(ctx context.Context, pfx *models.IPAMPrefix) error {
	if !pfx.PrefixType.Valid() {
		return fmt.Errorf("store: create prefix: invalid prefix_type %q: %w", pfx.PrefixType, models.ErrInvalidInput)
	}
	if pfx.Prefix == nil {
		return fmt.Errorf("store: create prefix: prefix is required: %w", models.ErrInvalidInput)
	}
	if pfx.ID == "" {
		pfx.ID = newUUID()
	}
	if pfx.ParentID == nil {
		net := net.IPNet(*pfx.Prefix)
		if parent, err := p.FindPrefixContaining(ctx, pfx.VRFID, &net); err == nil && parent != nil {
			pfx.ParentID = &parent.ID
		}
	}

	err := p.pool.QueryRow(ctx, `
		INSERT INTO ipam_prefixes (id, vrf_id, prefix, parent_id, prefix_type, name, description, utilization_percentage, vlan_id, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		RETURNING created_at, updated_at`,
		pfx.ID, pfx.VRFID, pfx.Prefix.String(), pfx.ParentID, pfx.PrefixType, pfx.Name, pfx.Description,
		pfx.UtilizationPercentage, pfx.VLANID, pfx.CreatedByUserID,
	).Scan(&pfx.CreatedAt, &pfx.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create prefix")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) UpdatePrefix(ctx context.Context, pfx *models.IPAMPrefix) error {
	if !pfx.PrefixType.Valid() {
		return fmt.Errorf("store: update prefix: invalid prefix_type %q: %w", pfx.PrefixType, models.ErrInvalidInput)
	}
	err := p.pool.QueryRow(ctx, `
		UPDATE ipam_prefixes SET name=$2, description=$3, prefix_type=$4, parent_id=$5, vlan_id=$6, updated_by_user_id=$7, updated_at=now()
		WHERE id = $1 RETURNING updated_at`,
		pfx.ID, pfx.Name, pfx.Description, pfx.PrefixType, pfx.ParentID, pfx.VLANID, pfx.UpdatedByUserID,
	).Scan(&pfx.UpdatedAt)
	if err != nil {
		return mapWriteError(mapReadError(err, "update prefix "+pfx.ID), "update prefix")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) DeletePrefix(ctx context.Context, id string) error {
	var childCount int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM ipam_prefixes WHERE parent_id = $1`, id).Scan(&childCount); err != nil {
		return fmt.Errorf("store: delete prefix %s: check children: %w", id, err)
	}
	if childCount > 0 {
		return fmt.Errorf("store: delete prefix %s: %d child prefixes: %w", id, childCount, models.ErrHasDependents)
	}
	tag, err := p.pool.Exec(ctx, `DELETE FROM ipam_prefixes WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete prefix")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete prefix %s: %w", id, models.ErrNotFound)
	}
	p.notifyConfigChanged(ctx)
	return nil
}

// GetIPAMTree assembles the tree rooted at parentID (nil for VRF top-level)
// down to depth levels via one recursive-CTE query, joining each node's
// immediate child count and whether a DHCP scope was carved from it.
func (p *Postgres) GetIPAMTree(ctx context.Context, vrfID string, parentID *string, depth int) ([]*models.IPAMTreeNode, error) {
	if depth < 1 {
		depth = 1
	}

	rows, err := p.pool.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT p.id, p.vrf_id, p.prefix, p.parent_id, p.prefix_type, p.name, p.description,
				p.utilization_percentage, p.vlan_id, p.created_at, p.updated_at, p.created_by_user_id, p.updated_by_user_id,
				0 AS level
			FROM ipam_prefixes p
			WHERE p.vrf_id = $1 AND (($2::uuid IS NULL AND p.parent_id IS NULL) OR p.parent_id = $2)
			UNION ALL
			SELECT c.id, c.vrf_id, c.prefix, c.parent_id, c.prefix_type, c.name, c.description,
				c.utilization_percentage, c.vlan_id, c.created_at, c.updated_at, c.created_by_user_id, c.updated_by_user_id,
				t.level + 1
			FROM ipam_prefixes c JOIN tree t ON c.parent_id = t.id
			WHERE t.level < $3 - 1
		)
		SELECT tree.id, tree.vrf_id, tree.prefix, tree.parent_id, tree.prefix_type, tree.name, tree.description,
			tree.utilization_percentage, tree.vlan_id, vl.vid, vl.name,
			tree.created_at, tree.updated_at, tree.created_by_user_id, tree.updated_by_user_id,
			tree.level,
			(SELECT count(*) FROM ipam_prefixes cc WHERE cc.parent_id = tree.id) AS child_count,
			-- How much of this node's CIDR its direct children cover. Always
			-- defined, unlike utilization_percentage (NULL without DHCP), so
			-- every tree node has a bar to draw.
			COALESCE((SELECT SUM(broadcast(cc.prefix::inet) - network(cc.prefix::inet) + 1)
				FROM ipam_prefixes cc WHERE cc.parent_id = tree.id), 0)::float8
				/ NULLIF((broadcast(tree.prefix::inet) - network(tree.prefix::inet) + 1)::float8, 0) * 100
				AS space_pct,
			ds.id AS scope_id
		FROM tree
		LEFT JOIN dhcp_scopes ds ON ds.ipam_prefix_id = tree.id
		LEFT JOIN vlans vl ON vl.id = tree.vlan_id
		ORDER BY tree.level, tree.prefix`, vrfID, parentID, depth)
	if err != nil {
		return nil, fmt.Errorf("store: get ipam tree: %w", err)
	}
	defer rows.Close()

	nodes := make(map[string]*models.IPAMTreeNode)
	// Non-nil so a VRF with zero matching prefixes serializes to JSON `[]`
	// rather than `null` (encoding/json's behavior for a nil slice) — the
	// frontend tree consumes this as a plain array.
	roots := make([]*models.IPAMTreeNode, 0)
	var order []struct {
		id       string
		parentID *string
		level    int
	}

	for rows.Next() {
		var ipnet net.IPNet
		var pfx models.IPAMPrefix
		var level, childCount int
		var scopeID *string
		var spacePct *float64
		if err := rows.Scan(&pfx.ID, &pfx.VRFID, &ipnet, &pfx.ParentID, &pfx.PrefixType, &pfx.Name, &pfx.Description,
			&pfx.UtilizationPercentage, &pfx.VLANID, &pfx.VLANVID, &pfx.VLANName,
			&pfx.CreatedAt, &pfx.UpdatedAt, &pfx.CreatedByUserID, &pfx.UpdatedByUserID,
			&level, &childCount, &spacePct, &scopeID); err != nil {
			return nil, fmt.Errorf("store: get ipam tree: %w", err)
		}
		n := models.IPNet(ipnet)
		pfx.Prefix = &n

		node := &models.IPAMTreeNode{
			Prefix:     &pfx,
			ChildCount: childCount,
			HasScope:   scopeID != nil,
			ScopeID:    scopeID,
		}
		if spacePct != nil {
			node.SpacePercentage = *spacePct
		}
		if level == depth-1 && childCount > 0 {
			node.ChildrenMore = true
		}
		nodes[pfx.ID] = node
		order = append(order, struct {
			id       string
			parentID *string
			level    int
		}{pfx.ID, pfx.ParentID, level})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get ipam tree: %w", err)
	}

	for _, o := range order {
		node := nodes[o.id]
		if o.level == 0 || o.parentID == nil {
			roots = append(roots, node)
			continue
		}
		if parent, ok := nodes[*o.parentID]; ok {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}

	return roots, nil
}

// NextAvailableSubnet finds the first unused /prefixLen block within
// parentPrefixID's range that doesn't overlap any existing descendant
// prefix.
func (p *Postgres) NextAvailableSubnet(ctx context.Context, parentPrefixID string, prefixLen int) (*models.IPNet, error) {
	parent, err := p.GetPrefixByID(ctx, parentPrefixID)
	if err != nil {
		return nil, fmt.Errorf("store: next available subnet: %w", err)
	}
	parentNet := net.IPNet(*parent.Prefix)
	parentOnes, bits := parentNet.Mask.Size()
	if prefixLen < parentOnes || prefixLen > bits {
		return nil, fmt.Errorf("store: next available subnet: /%d is not a valid subdivision of %s: %w", prefixLen, parent.Prefix, models.ErrInvalidInput)
	}

	descendants, err := p.allDescendantNets(ctx, parentPrefixID)
	if err != nil {
		return nil, fmt.Errorf("store: next available subnet: %w", err)
	}

	blockSize := new(big.Int).Lsh(big.NewInt(1), uint(bits-prefixLen))
	numBlocks := new(big.Int).Lsh(big.NewInt(1), uint(prefixLen-parentOnes))
	base := ipToBigInt(parentNet.IP)

	const maxScan = 1 << 20
	limit := numBlocks
	if numBlocks.Cmp(big.NewInt(maxScan)) > 0 {
		limit = big.NewInt(maxScan)
	}

	candidate := new(big.Int).Set(base)
	for i := big.NewInt(0); i.Cmp(limit) < 0; i.Add(i, big.NewInt(1)) {
		candIP := bigIntToIP(candidate, len(parentNet.IP))
		candNet := &net.IPNet{IP: candIP, Mask: net.CIDRMask(prefixLen, bits)}
		if !overlapsAny(candNet, descendants) {
			n := models.IPNet(*candNet)
			return &n, nil
		}
		candidate.Add(candidate, blockSize)
	}
	return nil, fmt.Errorf("store: next available subnet under %s: %w", parentPrefixID, models.ErrNoLeases)
}

// allDescendantNets returns every descendant (any depth) of parentID as
// *net.IPNet, for overlap checking.
func (p *Postgres) allDescendantNets(ctx context.Context, parentID string) ([]*net.IPNet, error) {
	rows, err := p.pool.Query(ctx, `
		WITH RECURSIVE descendants AS (
			SELECT id, prefix FROM ipam_prefixes WHERE parent_id = $1
			UNION ALL
			SELECT c.id, c.prefix FROM ipam_prefixes c JOIN descendants d ON c.parent_id = d.id
		)
		SELECT prefix FROM descendants`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*net.IPNet
	for rows.Next() {
		var ipnet net.IPNet
		if err := rows.Scan(&ipnet); err != nil {
			return nil, err
		}
		n := ipnet
		out = append(out, &n)
	}
	return out, rows.Err()
}

func overlapsAny(n *net.IPNet, others []*net.IPNet) bool {
	for _, o := range others {
		if networksOverlap(n, o) {
			return true
		}
	}
	return false
}

func networksOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

func ipToBigInt(ip net.IP) *big.Int {
	if v4 := ip.To4(); v4 != nil {
		return new(big.Int).SetBytes(v4)
	}
	return new(big.Int).SetBytes(ip.To16())
}

func bigIntToIP(v *big.Int, byteLen int) net.IP {
	b := v.Bytes()
	out := make([]byte, byteLen)
	copy(out[byteLen-len(b):], b)
	return net.IP(out)
}

// ---------------------------------------------------------------------------
// VRFStore
// ---------------------------------------------------------------------------

func (p *Postgres) ListVRFs(ctx context.Context, params ListParams) (ListResult[*models.VRF], error) {
	limit, offset := normalizeListParams(params)
	search := nilIfEmpty(params.Search)

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM vrfs WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')`, search).Scan(&total); err != nil {
		return ListResult[*models.VRF]{}, fmt.Errorf("store: count vrfs: %w", err)
	}

	order := orderByClause(params.Sort, map[string]string{"name": "name", "created_at": "created_at"}, "name ASC")
	rows, err := p.pool.Query(ctx, `SELECT `+vrfSelectCols+` FROM vrfs
		WHERE ($1::text IS NULL OR name ILIKE '%'||$1||'%')
		ORDER BY `+order+` LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return ListResult[*models.VRF]{}, fmt.Errorf("store: list vrfs: %w", err)
	}
	defer rows.Close()

	var items []*models.VRF
	for rows.Next() {
		v, err := scanVRF(rows)
		if err != nil {
			return ListResult[*models.VRF]{}, fmt.Errorf("store: list vrfs: %w", err)
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.VRF]{}, fmt.Errorf("store: list vrfs: %w", err)
	}
	return ListResult[*models.VRF]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (p *Postgres) CreateVRF(ctx context.Context, v *models.VRF) error {
	if v.ID == "" {
		v.ID = newUUID()
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO vrfs (id, name, route_distinguisher, description, is_default, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		RETURNING created_at, updated_at`,
		v.ID, v.Name, v.RouteDistinguisher, v.Description, v.IsDefault, v.CreatedByUserID,
	).Scan(&v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create vrf")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) UpdateVRF(ctx context.Context, v *models.VRF) error {
	err := p.pool.QueryRow(ctx, `
		UPDATE vrfs SET name=$2, route_distinguisher=$3, description=$4, is_default=$5, updated_by_user_id=$6, updated_at=now()
		WHERE id = $1 RETURNING updated_at`,
		v.ID, v.Name, v.RouteDistinguisher, v.Description, v.IsDefault, v.UpdatedByUserID,
	).Scan(&v.UpdatedAt)
	if err != nil {
		return mapWriteError(mapReadError(err, "update vrf "+v.ID), "update vrf")
	}
	p.notifyConfigChanged(ctx)
	return nil
}

func (p *Postgres) DeleteVRF(ctx context.Context, id string) error {
	var deps int
	if err := p.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM dhcp_scopes WHERE vrf_id = $1) + (SELECT count(*) FROM ipam_prefixes WHERE vrf_id = $1)
	`, id).Scan(&deps); err != nil {
		return fmt.Errorf("store: delete vrf %s: check dependents: %w", id, err)
	}
	if deps > 0 {
		return fmt.Errorf("store: delete vrf %s: %d dependent scopes/prefixes: %w", id, deps, models.ErrHasDependents)
	}
	tag, err := p.pool.Exec(ctx, `DELETE FROM vrfs WHERE id = $1`, id)
	if err != nil {
		return mapWriteError(err, "delete vrf")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete vrf %s: %w", id, models.ErrNotFound)
	}
	p.notifyConfigChanged(ctx)
	return nil
}

// scanDHCPClient scans a row from the ListClientsUnderPrefix UNION query
// into a models.DHCPClient. Columns must match the SELECT in ListClientsUnderPrefix.

// clientSelectCols is the column list every branch of the client UNION
// produces, in the order scanDHCPClient reads them. The two branches must
// agree on both order and type, so the NULL placeholders in each are cast
// explicitly -- an untyped NULL in a UNION takes the type of the first
// branch, which silently mis-types the column when that branch is also NULL.
const clientSelectCols = `id, kind, ip_address, pool_id, pool_name, scope_id, scope_name, prefix_id,
	client_mac, client_id, client_hostname, binding_state, lease_start, lease_end,
	binding_key, binding_type, is_enabled, created_at, updated_at`

// clientsCTE walks the prefix tree down from $1 and unions the leases and
// enabled reservations of every scope hanging off a prefix in that subtree.
// The descent matches the utilization rollup's (amendment A16.3): a scope
// belongs to a prefix when dhcp_scopes.ipam_prefix_id names it.
const clientsCTE = `
	WITH RECURSIVE descendants AS (
		SELECT id FROM ipam_prefixes WHERE id = $1
		UNION ALL
		SELECT p.id FROM ipam_prefixes p JOIN descendants d ON p.parent_id = d.id
	),
	clients AS (
		SELECT
			l.id::text                     AS id,
			'lease'::text                  AS kind,
			l.ip_address                   AS ip_address,
			l.pool_id::text                AS pool_id,
			po.name                        AS pool_name,
			s.id::text                     AS scope_id,
			s.name                         AS scope_name,
			s.ipam_prefix_id::text         AS prefix_id,
			l.client_mac                   AS client_mac,
			l.client_id                    AS client_id,
			l.client_hostname              AS client_hostname,
			l.binding_state                AS binding_state,
			l.lease_start                  AS lease_start,
			l.lease_end                    AS lease_end,
			NULL::text                     AS binding_key,
			NULL::text                     AS binding_type,
			NULL::boolean                  AS is_enabled,
			l.created_at                   AS created_at,
			l.updated_at                   AS updated_at
		FROM dhcp_leases l
		JOIN dhcp_pools po ON po.id = l.pool_id
		JOIN dhcp_scopes s ON s.id = po.scope_id
		WHERE s.ipam_prefix_id IN (SELECT id FROM descendants)

		UNION ALL

		SELECT
			r.id::text,
			'reservation'::text,
			r.reserved_ip,
			r.pool_id::text,
			po.name,
			s.id::text,
			s.name,
			s.ipam_prefix_id::text,
			-- A MAC-bound reservation names a hardware address, so surface it
			-- in the same column a lease uses. The value is only cast when it
			-- really looks like a MAC: a malformed legacy row must not abort
			-- the whole query with "invalid input syntax for type macaddr".
			CASE WHEN r.binding_key ~ '^mac:([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$'
				THEN substring(r.binding_key from 5)::macaddr END,
			NULL::bytea,
			r.hostname,
			NULL::text,
			NULL::timestamptz,
			NULL::timestamptz,
			r.binding_key,
			r.binding_type,
			r.is_enabled,
			r.created_at,
			r.updated_at
		FROM dhcp_reservations r
		JOIN dhcp_pools po ON po.id = r.pool_id
		JOIN dhcp_scopes s ON s.id = po.scope_id
		WHERE s.ipam_prefix_id IN (SELECT id FROM descendants) AND r.is_enabled
	)`

func scanDHCPClient(row pgx.Row) (*models.DHCPClient, error) {
	var c models.DHCPClient
	var clientMAC net.HardwareAddr
	var clientID []byte
	if err := row.Scan(
		&c.ID,
		&c.Kind,
		&c.IPAddress,
		&c.PoolID,
		&c.PoolName,
		&c.ScopeID,
		&c.ScopeName,
		&c.PrefixID,
		&clientMAC,
		&clientID,
		&c.ClientHostname,
		&c.BindingState,
		&c.LeaseStart,
		&c.LeaseEnd,
		&c.BindingKey,
		&c.BindingType,
		&c.IsEnabled,
		&c.CreatedAt,
		&c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(clientMAC) > 0 {
		ha := models.HardwareAddr(clientMAC)
		c.ClientMAC = &ha
	}
	c.ClientID = clientID
	return &c, nil
}

// ListClientsUnderPrefix returns a paginated list of DHCP clients (leases and
// enabled reservations) living in prefixID or any of its descendant prefixes.
// This is what lets the IPAM section answer "who is actually on this address
// space?" for a container or aggregate, without the operator walking down to
// every pool underneath it.
//
// Supports filters "kind" (lease|reservation), "state" (lease binding state)
// and "q" (free-text over IP / MAC / hostname), and sorts ip_address
// (default), lease_end, client_mac, client_hostname.
func (p *Postgres) ListClientsUnderPrefix(ctx context.Context, prefixID string, params ListParams) (ListResult[*models.DHCPClient], error) {
	limit, offset := normalizeListParams(params)

	// $1 is the prefix; filter placeholders continue from $2.
	args := []any{prefixID}
	where := ""
	addCond := func(cond string) {
		if where == "" {
			where = " WHERE " + cond
			return
		}
		where += " AND " + cond
	}

	if kind := params.Filters["kind"]; kind == string(models.ClientKindLease) || kind == string(models.ClientKindReservation) {
		args = append(args, kind)
		addCond(fmt.Sprintf("kind = $%d", len(args)))
	}
	if state := params.Filters["state"]; state != "" {
		// A lease-state filter cannot match a reservation, which has no
		// binding state at all, so reservations drop out.
		args = append(args, state)
		addCond(fmt.Sprintf("binding_state = $%d", len(args)))
	}
	// The handler exposes free text as the "q" filter; ParseListParams also
	// fills Search from a "search" query param. Accept either.
	q := params.Filters["q"]
	if q == "" {
		q = params.Search
	}
	if q = strings.TrimSpace(q); q != "" {
		args = append(args, "%"+q+"%")
		addCond(fmt.Sprintf(
			`(host(ip_address) ILIKE $%[1]d OR COALESCE(client_mac::text, '') ILIKE $%[1]d
				OR COALESCE(client_hostname, '') ILIKE $%[1]d)`, len(args)))
	}

	var total int64
	if err := p.pool.QueryRow(ctx, clientsCTE+` SELECT count(*) FROM clients`+where, args...).Scan(&total); err != nil {
		return ListResult[*models.DHCPClient]{}, fmt.Errorf("store: count clients under prefix %s: %w", prefixID, err)
	}

	order := orderByClause(params.Sort, map[string]string{
		"ip_address":      "ip_address",
		"lease_end":       "lease_end",
		"client_mac":      "client_mac",
		"client_hostname": "client_hostname",
	}, "ip_address ASC")

	args = append(args, limit, offset)
	rows, err := p.pool.Query(ctx, clientsCTE+` SELECT `+clientSelectCols+` FROM clients`+where+
		` ORDER BY `+order+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return ListResult[*models.DHCPClient]{}, fmt.Errorf("store: list clients under prefix %s: %w", prefixID, err)
	}
	defer rows.Close()

	clients := make([]*models.DHCPClient, 0, limit)
	for rows.Next() {
		c, err := scanDHCPClient(rows)
		if err != nil {
			return ListResult[*models.DHCPClient]{}, fmt.Errorf("store: scan client under prefix %s: %w", prefixID, err)
		}
		clients = append(clients, c)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.DHCPClient]{}, fmt.Errorf("store: list clients under prefix %s: %w", prefixID, err)
	}

	return ListResult[*models.DHCPClient]{Items: clients, Total: total, Limit: limit, Offset: offset}, nil
}

