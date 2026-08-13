-- Deja la base vacía. El orden importa menos por los CASCADE, pero se listan de
-- las dependientes a las principales para que un DROP falle ruidosamente si
-- alguien agrega una tabla y se olvida de sumarla acá.

DROP TABLE IF EXISTS expenses;
DROP TABLE IF EXISTS team_funds;
DROP TABLE IF EXISTS charges;

DROP TABLE IF EXISTS match_callups;
DROP TABLE IF EXISTS matches;
DROP TABLE IF EXISTS friendly_proposals;
DROP TABLE IF EXISTS friendly_challenges;
DROP TABLE IF EXISTS competition_invitations;
DROP TABLE IF EXISTS competition_entries;
DROP TABLE IF EXISTS competitions;

DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS fee_obligations;

DROP TABLE IF EXISTS join_requests;
DROP TABLE IF EXISTS team_invitations;
DROP TABLE IF EXISTS team_bank_accounts;
DROP TABLE IF EXISTS memberships;
DROP TABLE IF EXISTS teams;

DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS users;

-- pgcrypto no se borra: es una extensión de la base, no de este esquema, y
-- otra cosa podría estar usándola.
