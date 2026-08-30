-- Migration 0003: seed default admin user
--
-- Per amendment A5.3: username 'admin', password 'admin' (bcrypt hash
-- below), is_active = true, description explains this must be changed.
-- The hash was generated with golang.org/x/crypto/bcrypt.GenerateFromPassword
-- (cost 10) and verified against "admin" with bcrypt.CompareHashAndPassword
-- before being committed here.
--
-- Migration 0002 (IANA option catalog seed) is owned by a peer unit and is
-- not a dependency of this one — 0003 only touches the users table, created
-- in 0001.

BEGIN;

INSERT INTO users (username, email, password_hash, role, description, is_active)
VALUES (
  'admin',
  'admin@localhost',
  '$2a$10$Tbng6dSNholepKlwWwJLu.z0UIJsEEkxgSIeo1/6e4LlT8fH9lgSO',
  'admin',
  'Default admin — change this password',
  true
)
ON CONFLICT (username) DO NOTHING;

COMMIT;
