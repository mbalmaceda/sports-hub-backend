DROP TABLE IF EXISTS join_requests;
DROP TABLE IF EXISTS team_invitations;
DROP INDEX IF EXISTS users_tax_id_unique;
ALTER TABLE users DROP COLUMN IF EXISTS tax_id;
