-- El dueño de un equipo.
--
-- Hasta acá el equipo no tenía dueño: tenía managers, y "quién lo creó" solo se
-- podía adivinar por el manager más antiguo. Hace falta por dos razones:
--
--   · Es lo que mide el plan. "Gratis: dueño de 1 equipo" cuenta equipos
--     propios, no equipos donde uno es manager — un club con tres managers no
--     puede gastarle el cupo a los tres.
--   · Ordena "mis equipos": el propio va primero, en la lista y como equipo
--     activo por defecto.
--
-- Es una columna y no un rol a propósito. El rol dice qué puede hacer alguien
-- dentro del equipo; el dueño es de quién es el equipo. Son ejes distintos,
-- como `kind` y `plays_as_player`: el dueño hoy es manager, pero nada obliga a
-- que lo siga siendo.
--
-- Nullable porque el backfill puede no encontrar a nadie (un equipo sin
-- managers activos), y porque borrar al usuario no tiene que borrar el equipo.
ALTER TABLE teams ADD COLUMN owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL;

-- Los equipos que ya existen: el manager más antiguo, que es quien lo creó
-- (POST /teams lo deja como manager en el mismo request).
UPDATE teams t SET owner_user_id = (
    SELECT m.user_id FROM memberships m
    WHERE m.team_id = t.id AND m.role = 'manager' AND m.kind = 'member'
    ORDER BY m.joined_at
    LIMIT 1
);

CREATE INDEX teams_owner_user_id_idx ON teams (owner_user_id) WHERE owner_user_id IS NOT NULL;
