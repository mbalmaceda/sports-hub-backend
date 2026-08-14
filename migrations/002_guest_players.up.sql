-- ─── Invitados de un partido ("parches") ─────────────────────────────────────
--
-- Gente que no es del equipo y viene a completar una convocatoria. Es lo más
-- común del fútbol amateur y hasta acá la app no lo modelaba: para jugar había
-- que ser del plantel.
--
-- El invitado es una MEMBRESÍA, no una entidad aparte. Todo lo que sigue de
-- jugar cuelga de membership_id —la convocatoria, el cargo de la cancha, el
-- historial de asistencia—, así que una tabla paralela obligaba a duplicar el
-- flujo de cobros entero: comprobantes, reversas y reportes por segunda vez.
-- Con esto, el parche es un membership_id más y nada de eso se entera.

-- kind es la tercera pregunta sobre una membresía, y es ortogonal a las otras
-- dos: `role` es qué puede hacer con el equipo, `plays_as_player` si ocupa un
-- lugar en el plantel, y `kind` si es del equipo o está de paso.
--
-- Mezclarla con las otras dos es el error que esta tabla ya cometió una vez
-- (ver el comentario de memberships en la 001). Un invitado juega —ocupa un
-- lugar en la nómina de ese partido y paga su cuota de cancha— así que
-- plays_as_player queda en TRUE: lo que lo distingue no es que no juegue, es
-- que no es del club.
ALTER TABLE memberships
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'member'
        CHECK (kind IN ('member', 'guest'));

-- Enlace de invitación a un partido.
--
-- A diferencia de team_invitations, esta invitación existe ANTES que la
-- persona: por eso no hay user_id. Lo que identifica al invitado es el token,
-- que viaja por WhatsApp y lo canjea quien lo reciba.
CREATE TABLE match_guest_invites (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- SHA-256, nunca el valor en claro: el enlace es una credencial al
    -- portador, igual que un refresh token. Quien lea la tabla no puede usar
    -- lo que ve.
    token_hash         TEXT        NOT NULL UNIQUE,
    match_id           UUID        NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    -- Cuál de los dos equipos invita. El partido tiene dos lados y el cargo de
    -- la cancha sale del equipo que convoca, así que no se puede deducir.
    team_id            UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    created_by_user_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- El enlace nace capado a cuánta gente falta y muere cuando empieza el
    -- partido. Las dos cosas juntas son lo que evita el link huérfano que
    -- sigue circulando en un grupo de WhatsApp tres meses después: nadie tiene
    -- que acordarse de revocarlo.
    max_uses           SMALLINT    NOT NULL CHECK (max_uses > 0),
    used_count         SMALLINT    NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    expires_at         TIMESTAMPTZ NOT NULL,
    revoked_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT match_guest_invites_within_cap CHECK (used_count <= max_uses)
);

-- La búsqueda por token es la del canje, y es la única que importa.
CREATE INDEX ON match_guest_invites (match_id);
