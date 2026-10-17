DROP INDEX IF EXISTS friendly_searches_open_idx;
CREATE INDEX friendly_searches_open_idx ON friendly_searches (start_at) WHERE status = 'open';

ALTER TABLE friendly_searches DROP COLUMN IF EXISTS expires_at;
