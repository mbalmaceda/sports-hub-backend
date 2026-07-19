-- El rol (player/treasurer/manager) es por membership, no global del usuario:
-- un usuario puede ser manager de un equipo y solo player de otro.
-- No hay data productiva todavía.

ALTER TABLE memberships ADD COLUMN role TEXT NOT NULL DEFAULT 'player'
    CHECK (role IN ('player', 'treasurer', 'manager'));

ALTER TABLE users DROP COLUMN role;
