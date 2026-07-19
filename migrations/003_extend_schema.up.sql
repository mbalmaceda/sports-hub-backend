-- Campos adicionales en users para el perfil
ALTER TABLE users ADD COLUMN phone TEXT;
ALTER TABLE users ADD COLUMN avatar_url TEXT;

-- Teams extendido para matchear el tipo Team del mobile
ALTER TABLE teams ADD COLUMN sport_id   TEXT    NOT NULL DEFAULT '';
ALTER TABLE teams ADD COLUMN club_id    TEXT;
ALTER TABLE teams ADD COLUMN category   TEXT    NOT NULL DEFAULT '';
ALTER TABLE teams ADD COLUMN logo_url   TEXT;
ALTER TABLE teams ADD COLUMN fee_amount BIGINT  NOT NULL DEFAULT 0;
ALTER TABLE teams ADD COLUMN fee_due_day SMALLINT NOT NULL DEFAULT 1 CHECK (fee_due_day BETWEEN 1 AND 31);
ALTER TABLE teams ADD COLUMN currency   TEXT    NOT NULL DEFAULT 'USD';
ALTER TABLE teams ADD COLUMN is_active  BOOLEAN NOT NULL DEFAULT true;

-- Memberships con datos del jugador
ALTER TABLE memberships ADD COLUMN jersey_number SMALLINT;
ALTER TABLE memberships ADD COLUMN position TEXT;
