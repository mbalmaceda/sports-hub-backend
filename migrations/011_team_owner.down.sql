DROP INDEX IF EXISTS teams_owner_user_id_idx;
ALTER TABLE teams DROP COLUMN IF EXISTS owner_user_id;
