-- ─── Enlace para unirse al equipo ───────────────────────────────────────────
--
-- El cuarto camino para entrar a un equipo, y el que faltaba para que uno
-- nuevo crezca. Los otros dos de ingreso al club —team_invitations y
-- join_requests— apuntan a alguien que ya tiene cuenta (`user_id NOT NULL`): el
-- manager tiene que conocer su correo, o la persona tiene que encontrar el
-- equipo. Este enlace, como el de los parches (002), existe ANTES que la
-- persona: lo que la identifica es el token, que viaja por WhatsApp.
--
-- La diferencia con match_guest_invites es a qué se entra. El parche entra a un
-- partido y su vínculo se apaga con él; quien canjea este enlace entra al club,
-- con kind = 'member', igual que si hubiera aceptado una invitación por correo.
-- Por eso son dos tablas y no una con un tipo: cada una crea una membresía
-- distinta, y una columna que lo decida es la clase de bifurcación que un día
-- se lee al revés y le da plantel a un parche.
CREATE TABLE team_invite_links (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- SHA-256, nunca el valor en claro: el enlace es una credencial al
    -- portador. Quien lea la tabla no puede usar lo que ve.
    token_hash         TEXT        NOT NULL UNIQUE,
    team_id            UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    created_by_user_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Capado y con vencimiento por lo mismo que el de los parches: lo que
    -- evita el enlace que sigue abriendo la puerta del club meses después en un
    -- grupo de WhatsApp es que muera solo, no que alguien se acuerde de anularlo.
    -- Los valores (7 días, 30 personas) los fija el handler.
    max_uses           SMALLINT    NOT NULL CHECK (max_uses > 0),
    used_count         SMALLINT    NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    expires_at         TIMESTAMPTZ NOT NULL,
    revoked_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT team_invite_links_within_cap CHECK (used_count <= max_uses)
);

-- La pantalla de Invitar jugador lista los enlaces del equipo.
CREATE INDEX ON team_invite_links (team_id);
