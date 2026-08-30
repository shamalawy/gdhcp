-- 0006_lease_reservation_state.sql -- amendment A19.
--
-- Two changes, both in service of one thing: an operator must be able to see
-- whether an address is pinned before trying to pin it.
--
-- 1. lease_binding_keys() puts the canonical binding-key forms of a lease row
--    in ONE place in SQL. models.ClientIdentity.BindingKeys() is the Go side
--    of the same contract; the two must agree exactly, because a key built
--    differently here would silently fail to match the reservation the
--    allocation path matched -- which is the class of defect A17.1 already
--    fixed once, in the write path.
--
-- 2. An index on (pool_id, reserved_ip). The address side of the reservation
--    lookup had no supporting index: UNIQUE (pool_id, binding_key) covers
--    only the client side.

-- Canonical binding keys for a lease row, in BindingTypePrecedence order
-- (client_id, then mac, then hostname). A lease only ever captures those
-- three of the seven identifier types -- the other four (circuit_id,
-- remote_id, vendor_class, user_class) live in the packet and are never
-- persisted on dhcp_leases, so a lease can never be matched by them here.
--
-- Value forms mirror models.CanonicalBindingValue:
--   mac       -- lowercase colon form. macaddr::text is already lowercase;
--                lower() is belt and braces against a server whose output
--                format differs.
--   client_id -- lowercase hex of the raw option 61 bytes.
--   hostname  -- trimmed and lowercased.
CREATE OR REPLACE FUNCTION lease_binding_keys(
  p_mac      MACADDR,
  p_clientid BYTEA,
  p_hostname VARCHAR
) RETURNS TEXT[]
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
  SELECT array_remove(ARRAY[
    CASE WHEN p_clientid IS NOT NULL AND octet_length(p_clientid) > 0
         THEN 'client_id:' || encode(p_clientid, 'hex') END,
    CASE WHEN p_mac IS NOT NULL
         THEN 'mac:' || lower(p_mac::text) END,
    CASE WHEN p_hostname IS NOT NULL AND btrim(p_hostname) <> ''
         THEN 'hostname:' || lower(btrim(p_hostname)) END
  ], NULL)
$$;

COMMENT ON FUNCTION lease_binding_keys(MACADDR, BYTEA, VARCHAR) IS
  'Canonical reservation binding keys for a dhcp_leases row, in binding-type '
  'precedence order. Mirrors models.ClientIdentity.BindingKeys(); amendment A19.';

-- The reservation lookup for a lease matches on the client side
-- (binding_key, already indexed by UNIQUE (pool_id, binding_key)) OR on the
-- address side, which had only a pool-wide index on reserved_ip alone.
CREATE INDEX IF NOT EXISTS idx_dhcp_reservations_pool_reserved_ip
  ON dhcp_reservations (pool_id, reserved_ip);
