-- Migration 0004: VLAN groups, VLANs, and VLAN assignment on IPAM prefixes.
--
-- Layer-2 segmentation was missing entirely: VRFs model routing-domain
-- isolation, but nothing recorded which VLAN a subnet lives on. Operators
-- work in VLAN terms daily, so IPAM prefixes gain an optional VLAN.
--
-- VLAN groups exist so the same VID can be reused in different L2 domains
-- (VID 100 at two sites is two distinct VLANs). Ungrouped VLANs share one
-- implicit global domain, hence the two partial unique indexes below rather
-- than a single UNIQUE(group_id, vid) — NULL never equals NULL in SQL, so a
-- plain constraint would let duplicate ungrouped VIDs through.

CREATE TABLE vlan_groups (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) UNIQUE NOT NULL,
  description TEXT,
  -- Optional: scope a group to one routing domain. SET NULL (not CASCADE)
  -- so deleting a VRF does not silently delete its VLAN definitions.
  vrf_id UUID REFERENCES vrfs(id) ON DELETE SET NULL,
  min_vid INTEGER NOT NULL DEFAULT 1    CHECK (min_vid BETWEEN 1 AND 4094),
  max_vid INTEGER NOT NULL DEFAULT 4094 CHECK (max_vid BETWEEN 1 AND 4094),
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT vlan_group_vid_range CHECK (min_vid <= max_vid)
);

CREATE INDEX idx_vlan_groups_name ON vlan_groups(name);
CREATE INDEX idx_vlan_groups_vrf ON vlan_groups(vrf_id);

CREATE TABLE vlans (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id UUID REFERENCES vlan_groups(id) ON DELETE SET NULL,
  -- 802.1Q: 0 and 4095 are reserved, so the usable range is 1-4094.
  vid INTEGER NOT NULL CHECK (vid BETWEEN 1 AND 4094),
  name VARCHAR(255) NOT NULL,
  description TEXT,
  status VARCHAR(20) NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'reserved', 'deprecated')),
  created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

-- A VID is unique within its group; ungrouped VLANs share one global domain.
CREATE UNIQUE INDEX vlans_group_vid  ON vlans(group_id, vid) WHERE group_id IS NOT NULL;
CREATE UNIQUE INDEX vlans_global_vid ON vlans(vid)           WHERE group_id IS NULL;
CREATE INDEX idx_vlans_group ON vlans(group_id);
CREATE INDEX idx_vlans_vid ON vlans(vid);

-- Assignment. SET NULL so deleting a VLAN definition unassigns prefixes
-- rather than deleting address space along with it.
ALTER TABLE ipam_prefixes
  ADD COLUMN vlan_id UUID REFERENCES vlans(id) ON DELETE SET NULL;

CREATE INDEX idx_ipam_prefixes_vlan ON ipam_prefixes(vlan_id);

-- entity_tags.entity_type is a free-text VARCHAR with only a comment for
-- documentation; 'prefix' and 'vlan' join the accepted set (the API-side
-- validation in api/tags/handler.go is the real gate).
COMMENT ON COLUMN entity_tags.entity_type IS
  'vrf, prefix, vlan, scope, pool, reservation, server, cluster';
