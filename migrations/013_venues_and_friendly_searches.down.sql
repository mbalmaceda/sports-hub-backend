DROP INDEX IF EXISTS friendly_challenges_search_requester_key;
ALTER TABLE friendly_challenges DROP COLUMN IF EXISTS search_id;

DROP TABLE IF EXISTS friendly_searches;

ALTER TABLE competitions DROP COLUMN IF EXISTS venue_id;

DROP TABLE IF EXISTS venues;
