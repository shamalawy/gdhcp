-- Migration 0005: repair reservation binding keys and widen the identifier set.
--
-- Two defects are fixed here.
--
-- 1. Every reservation ever written through the REST API, the web UI or the
--    CLI stored binding_key as a BARE value ("aa:bb:cc:dd:ee:ff") while the
--    allocation engine looks a reservation up by its type-prefixed candidates
--    ("mac:aa:bb:cc:dd:ee:ff", "client_id:<hex>", ...). The two never met, so
--    reservations silently did nothing: the client simply got a dynamic
--    address and no error was reported anywhere. The write paths now
--    canonicalise through models.BuildBindingKey; this migration repairs the
--    rows already on disk.
--
-- 2. Only 'mac', 'client_id' and 'hostname' were accepted, and 'hostname' was
--    dead on arrival because the engine never read option 12. The engine now
--    reads options 12, 60 and 77 and reuses the option 82 circuit/remote IDs
--    it already parsed for logging, so four further identifier types become
--    matchable and are admitted by the CHECK constraint below.
--
-- Both statements are idempotent: re-running cannot double-prefix a key,
-- because a row is only rewritten when it does not already carry its own
-- type prefix.

BEGIN;

-- Legacy rows: prefix the stored value with its binding_type. MAC values are
-- also lowercased, since net.HardwareAddr.String() -- the form the engine
-- builds its candidates in -- is always lowercase, and a key stored in upper
-- case would miss for exactly the same reason the missing prefix did.
UPDATE dhcp_reservations
SET binding_key = binding_type || ':' ||
      CASE WHEN binding_type = 'mac' THEN lower(binding_key) ELSE binding_key END,
    updated_at = now()
WHERE binding_key NOT LIKE binding_type || ':%';

-- Already-prefixed MAC rows written before canonicalisation could still hold
-- an upper-case address.
UPDATE dhcp_reservations
SET binding_key = lower(binding_key),
    updated_at = now()
WHERE binding_type = 'mac' AND binding_key <> lower(binding_key);

-- Admit the four identifier types the engine can now match. Order matches
-- models.BindingTypePrecedence for readability only; the constraint is a set.
ALTER TABLE dhcp_reservations DROP CONSTRAINT valid_binding_type;
ALTER TABLE dhcp_reservations ADD CONSTRAINT valid_binding_type
  CHECK (binding_type IN ('client_id', 'mac', 'circuit_id', 'remote_id',
                          'vendor_class', 'user_class', 'hostname'));

COMMENT ON COLUMN dhcp_reservations.binding_key IS
  'Canonical type-prefixed client identity, "<binding_type>:<canonical value>" '
  '(e.g. "mac:aa:bb:cc:dd:ee:ff", "circuit_id:<hex>"). Built by '
  'models.BuildBindingKey; the allocation engine matches these against the '
  'candidates it derives from each request.';

COMMIT;
