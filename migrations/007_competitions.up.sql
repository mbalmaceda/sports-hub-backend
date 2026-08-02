-- Competencias: el eje del MVP.
--
-- Un amistoso y un torneo son la misma cosa con distinta cantidad de equipos y
-- distintas reglas de avance, así que comparten tabla. Lo que cambia —la
-- negociación de fecha de un amistoso, el fixture de un torneo— vive aparte.

CREATE TABLE competitions (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    sport_id          TEXT        NOT NULL,
    type              TEXT        NOT NULL CHECK (type IN ('friendly', 'tournament', 'league')),
    name              TEXT        NOT NULL,
    organizer_team_id UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    status            TEXT        NOT NULL DEFAULT 'draft'
                                  CHECK (status IN ('draft', 'active', 'finished', 'cancelled')),
    start_at          TIMESTAMPTZ,
    end_at            TIMESTAMPTZ,
    -- Desnormalizado a propósito: la lista de competencias del mobile muestra
    -- el lugar en cada tarjeta, y resolverlo desde las propuestas obligaría a
    -- una consulta por competencia solo para pintar una línea de texto.
    venue             TEXT,
    players_per_side  SMALLINT,
    -- En unidades menores de la moneda. El peso chileno no tiene decimales:
    -- tratarlo como centavos infla los totales ×100.
    venue_cost_amount BIGINT,
    venue_cost_currency TEXT,
    rules             JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Qué equipos participan y en qué estado está su participación.
CREATE TABLE competition_entries (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    competition_id UUID        NOT NULL REFERENCES competitions(id) ON DELETE CASCADE,
    team_id        UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    status         TEXT        NOT NULL DEFAULT 'invited'
                               CHECK (status IN ('invited', 'pending', 'active', 'declined', 'withdrawn')),
    joined_at      TIMESTAMPTZ,
    UNIQUE (competition_id, team_id)
);

-- Invitación formal de un equipo a otro para entrar a una competencia.
CREATE TABLE competition_invitations (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    competition_id UUID        NOT NULL REFERENCES competitions(id) ON DELETE CASCADE,
    from_team_id   UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    to_team_id     UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    status         TEXT        NOT NULL DEFAULT 'sent'
                               CHECK (status IN ('sent', 'accepted', 'declined', 'expired', 'revoked')),
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at   TIMESTAMPTZ
);

-- Índice parcial: solo puede haber UNA invitación abierta por par de equipos y
-- competencia. Sin esto, tocar dos veces "Invitar" manda dos avisos por lo mismo.
CREATE UNIQUE INDEX competition_invitations_open_unique
    ON competition_invitations (competition_id, to_team_id)
    WHERE status = 'sent';

-- ─── Amistosos ───────────────────────────────────────────────────────────────
-- La negociación es lo propio del amistoso: se propone fecha y lugar, el rival
-- contraoferta, y así hasta que alguien acepta o expira.

CREATE TABLE friendly_challenges (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    competition_id     UUID        NOT NULL REFERENCES competitions(id) ON DELETE CASCADE,
    challenger_team_id UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    challenged_team_id UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    status             TEXT        NOT NULL DEFAULT 'pending'
                                   CHECK (status IN ('pending', 'countered', 'accepted', 'declined', 'expired', 'cancelled')),
    expires_at         TIMESTAMPTZ NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (challenger_team_id <> challenged_team_id)
);

-- Cada vuelta de la negociación. Se conservan todas: el historial es lo que la
-- app muestra como línea de tiempo, y sirve para saber en qué quedó la cosa.
CREATE TABLE friendly_proposals (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    challenge_id        UUID        NOT NULL REFERENCES friendly_challenges(id) ON DELETE CASCADE,
    proposed_by_team_id UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    proposed_start_at   TIMESTAMPTZ NOT NULL,
    proposed_venue      TEXT,
    message             TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Partidos ────────────────────────────────────────────────────────────────

CREATE TABLE matches (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    competition_id UUID        NOT NULL REFERENCES competitions(id) ON DELETE CASCADE,
    home_team_id   UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    away_team_id   UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    scheduled_at   TIMESTAMPTZ NOT NULL,
    venue          TEXT,
    status         TEXT        NOT NULL DEFAULT 'draft'
                               CHECK (status IN ('draft', 'confirmed', 'completed', 'cancelled')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (home_team_id <> away_team_id)
);

-- Convocatoria: el manager llama, el jugador responde.
--
-- Es entidad propia y no un campo dentro del partido porque de acá salen el
-- historial de asistencia y las estadísticas por jugador. Guardado como JSON
-- adentro del partido, eso sería inconsultable.
CREATE TABLE match_callups (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    match_id      UUID        NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    membership_id UUID        NOT NULL REFERENCES memberships(id) ON DELETE CASCADE,
    status        TEXT        NOT NULL DEFAULT 'called'
                              CHECK (status IN ('called', 'confirmed', 'declined')),
    called_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at  TIMESTAMPTZ,
    UNIQUE (match_id, membership_id)
);

-- 'not-called' no se guarda: es la ausencia de fila. Un jugador del plantel sin
-- convocatoria está sin convocar, y así el estado no se puede desincronizar
-- del plantel real.

CREATE INDEX ON competitions (organizer_team_id);
CREATE INDEX ON competitions (status);
CREATE INDEX ON competition_entries (team_id);
CREATE INDEX ON competition_invitations (to_team_id, status);
CREATE INDEX ON friendly_challenges (challenger_team_id);
CREATE INDEX ON friendly_challenges (challenged_team_id);
CREATE INDEX ON friendly_challenges (status);
CREATE INDEX ON friendly_proposals (challenge_id, created_at);
CREATE INDEX ON matches (competition_id);
CREATE INDEX ON matches (home_team_id, scheduled_at);
CREATE INDEX ON matches (away_team_id, scheduled_at);
CREATE INDEX ON match_callups (membership_id);
