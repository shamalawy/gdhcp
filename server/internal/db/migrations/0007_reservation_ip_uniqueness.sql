-- 0007_reservation_ip_uniqueness.sql
--
-- One address, one reservation -- enforced by the database rather than by
-- three SELECTs and a hope.
--
-- CreateReservation/UpdateReservation guard against a duplicate reservation
-- with ReservationByBindingKey, ReservationByIP and rejectConflictingActiveLease
-- before inserting. Those run as separate statements at READ COMMITTED with
-- no transaction around them, so two concurrent creates for the same address
-- both see "free" and both insert. The client side of the pair was already
-- safe -- 0001 declares UNIQUE (pool_id, binding_key) -- but the address side
-- had only the non-unique idx_dhcp_reservations_reserved_ip, so nothing at
-- the storage layer stopped the race.
--
-- Two reservations on one address is not a cosmetic duplicate: amendment A6
-- step 1 matches a reservation before anything else and wins
-- unconditionally, so whichever client asks second is pinned to an address
-- the first already owns and can never be handed a lease at all.
--
-- The guards stay: they still turn the ordinary case into a precise
-- ErrReservationIPTaken instead of a raw constraint name. They are now an
-- error-message optimisation, and this index is the actual invariant.
--
-- If this migration fails with a uniqueness error, the database already
-- holds duplicate reservations created by that race. They must be resolved
-- by hand -- gdhcp cannot know which of the two clients was meant to own the
-- address. To find them:
--
--   SELECT pool_id, reserved_ip, count(*), array_agg(binding_key)
--   FROM dhcp_reservations GROUP BY pool_id, reserved_ip HAVING count(*) > 1;
--
-- The index supersedes 0006's idx_dhcp_reservations_pool_reserved_ip, which
-- covered the same columns non-uniquely; it is dropped so the table does not
-- carry two indexes on one key.

CREATE UNIQUE INDEX IF NOT EXISTS uq_dhcp_reservations_pool_reserved_ip
  ON dhcp_reservations (pool_id, reserved_ip);

DROP INDEX IF EXISTS idx_dhcp_reservations_pool_reserved_ip;
