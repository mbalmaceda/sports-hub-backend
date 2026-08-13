-- Esquema base de ZPORTS.
--
-- Consolida las 20 migraciones incrementales que existieron hasta antes del
-- lanzamiento. El historial de cómo se llegó hasta acá está en git; lo que se
-- conserva en los comentarios es por qué el esquema es como es, que es lo único
-- que sigue siendo útil de acá en adelante.
--
-- Requiere una base vacía.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ─── Personas ────────────────────────────────────────────────────────────────

-- users no tiene rol: el rol es por membership, porque una persona puede ser
-- manager de un equipo y solo jugador de otro.
CREATE TABLE users (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT        NOT NULL,
    email          TEXT        NOT NULL UNIQUE,
    password_hash  TEXT        NOT NULL DEFAULT '',
    phone          TEXT,
    avatar_url     TEXT,
    -- Identificación tributaria: RUT en Chile, CUIT en Argentina, CPF en
    -- Brasil. Se llama tax_id y no rut porque es el campo con el que un manager
    -- busca a un jugador para invitarlo, y ese buscador tiene que seguir
    -- sirviendo cuando la app cruce la cordillera.
    tax_id         TEXT,
    push_token     TEXT,

    -- Perfil deportivo, todo opcional: la cuenta se crea sin completarlo.
    favorite_sport TEXT,
    height_cm      INTEGER     CHECK (height_cm > 0),
    weight_kg      NUMERIC(5,1) CHECK (weight_kg > 0),
    birth_date     DATE,
    alias          TEXT,
    city           TEXT,
    dominant_side  TEXT        CHECK (dominant_side IN ('right', 'left', 'both')),
    bio            TEXT,

    -- Borrado de cuenta (requisito de Google Play). La fila no se borra: se
    -- anonimiza y se marca acá. payments.recorded_by apunta a users con ON
    -- DELETE CASCADE, así que un DELETE físico del tesorero se llevaría por
    -- delante los pagos que registró de OTROS jugadores y dejaría la
    -- contabilidad del equipo con agujeros.
    deleted_at     TIMESTAMPTZ,

    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- El UNIQUE de la columna es sensible a mayúsculas, así que no alcanza:
-- 'Mirko@x.com' y 'mirko@x.com' serían dos cuentas. Este índice es el que
-- garantiza una cuenta por email. Las cuentas dadas de baja quedan fuera porque
-- el borrado les reescribe el email y no tienen por qué bloquear un registro
-- nuevo.
CREATE UNIQUE INDEX users_email_lower_key
    ON users (lower(email))
    WHERE deleted_at IS NULL;

-- Único admitiendo NULL: no todos cargan el dato, pero dos personas no pueden
-- compartir el mismo. El índice además hace rápida la búsqueda exacta, que es
-- la única que se permite.
CREATE UNIQUE INDEX users_tax_id_unique ON users (tax_id) WHERE tax_id IS NOT NULL;

-- ─── Sesiones ────────────────────────────────────────────────────────────────

-- Refresh tokens opacos, guardados como SHA-256. Nunca se almacena el valor en
-- claro: quien lea la tabla no puede usar lo que ve.
--
-- family_id une los eslabones de una cadena de sesión: al rotar, el token nuevo
-- hereda la familia del viejo. Eso permite matar una sesión entera —todo lo que
-- desciende de un mismo login— cuando se detecta que alguien reutilizó un token
-- ya rotado, que es la única señal de robo disponible.
CREATE TABLE refresh_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id  UUID        NOT NULL DEFAULT gen_random_uuid(),
    token_hash TEXT        NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    -- Se marca al rotar. Un token usado que reaparece pasada la ventana de
    -- gracia es una reutilización.
    used_at    TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON refresh_tokens (user_id);
CREATE INDEX ON refresh_tokens (family_id);
-- Lo usa el reaper, que barre por vencimiento.
CREATE INDEX ON refresh_tokens (expires_at);

-- ─── Equipos ─────────────────────────────────────────────────────────────────

CREATE TABLE teams (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    sport_id    TEXT        NOT NULL DEFAULT '',
    club_id     TEXT,
    category    TEXT        NOT NULL DEFAULT '',
    logo_url    TEXT,
    fee_amount  BIGINT      NOT NULL DEFAULT 0,
    fee_due_day SMALLINT    NOT NULL DEFAULT 1 CHECK (fee_due_day BETWEEN 1 AND 31),
    currency    TEXT        NOT NULL DEFAULT 'USD',
    is_active   BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Por nombre en minúsculas: "Deportivo Norte" y "deportivo norte" serían el
-- mismo equipo para quien busca, y el buscador mostraría duplicados.
CREATE UNIQUE INDEX teams_name_lower_key ON teams (lower(name));

-- role es qué puede hacer la persona con el equipo (administrar, cobrar);
-- plays_as_player, si ocupa un lugar en el plantel. Son cosas distintas: el que
-- organiza muchas veces también juega, y mezclarlas dejaba a los managers fuera
-- de las plantillas, las convocatorias y las cuotas.
CREATE TABLE memberships (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id         UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    role            TEXT        NOT NULL DEFAULT 'player'
                                CHECK (role IN ('player', 'treasurer', 'manager')),
    plays_as_player BOOLEAN     NOT NULL DEFAULT TRUE,
    status          TEXT        NOT NULL DEFAULT 'pending',
    jersey_number   SMALLINT,
    position        TEXT,
    joined_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, team_id)
);

CREATE INDEX ON memberships (user_id);
CREATE INDEX ON memberships (team_id);

-- Datos bancarios a los que los jugadores le transfieren al equipo.
--
-- Los campos son deliberadamente genéricos y no chilenos: `holder_tax_id` en
-- vez de `rut`, `account_type` como texto en vez de un enum con las cuentas de
-- un solo país. El mismo modelo tiene que servir para el CUIT argentino y el
-- CPF brasileño sin migrar la tabla.
CREATE TABLE team_bank_accounts (
    team_id        UUID        PRIMARY KEY REFERENCES teams(id) ON DELETE CASCADE,
    bank_name      TEXT        NOT NULL,
    account_type   TEXT        NOT NULL,
    account_number TEXT        NOT NULL,
    holder_name    TEXT        NOT NULL,
    holder_tax_id  TEXT        NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Incorporación de jugadores ──────────────────────────────────────────────
--
-- Hay dos caminos, simétricos pero distintos, y se modelan aparte:
--
--   team_invitations → el equipo busca a la persona y la invita. Acepta la persona.
--   join_requests    → la persona encuentra al equipo y pide entrar. Acepta el equipo.
--
-- Colapsarlos en una tabla con un campo "dirección" obliga a preguntar quién
-- puede aceptar en cada consulta, y ese es justo el chequeo que no hay que
-- equivocar: si se invierte, cualquiera se mete solo a cualquier equipo.

CREATE TABLE team_invitations (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id            UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    invited_by_user_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_id            UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status             TEXT        NOT NULL DEFAULT 'sent'
                                   CHECK (status IN ('sent', 'accepted', 'declined', 'expired', 'revoked')),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at       TIMESTAMPTZ
);

-- Una sola invitación abierta por persona y equipo: si no, tocar dos veces
-- "Invitar" manda dos avisos por lo mismo y duplica la lista del manager.
CREATE UNIQUE INDEX team_invitations_open_unique
    ON team_invitations (team_id, user_id) WHERE status = 'sent';

CREATE TABLE join_requests (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id      UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message      TEXT,
    status       TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'accepted', 'declined', 'cancelled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMPTZ,
    resolved_by  UUID        REFERENCES users(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX join_requests_open_unique
    ON join_requests (team_id, user_id) WHERE status = 'pending';

CREATE INDEX ON team_invitations (user_id, status);
CREATE INDEX ON join_requests (team_id, status);

-- ─── Cuotas mensuales ────────────────────────────────────────────────────────
--
-- Convive con `charges`: la migración de las cuotas al ledger unificado es un
-- paso pendiente, no algo que este esquema dé por hecho.

CREATE TABLE fee_obligations (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id       UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    membership_id UUID        NOT NULL REFERENCES memberships(id) ON DELETE CASCADE,
    period_year   SMALLINT    NOT NULL,
    period_month  SMALLINT    NOT NULL CHECK (period_month BETWEEN 1 AND 12),
    amount        BIGINT      NOT NULL,
    currency      TEXT        NOT NULL DEFAULT 'USD',
    due_date      DATE        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'pending',
    paid_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (membership_id, period_year, period_month)
);

CREATE TABLE payments (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id       UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    obligation_id UUID        REFERENCES fee_obligations(id) ON DELETE SET NULL,
    payer_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recorded_by   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount        BIGINT      NOT NULL,
    currency      TEXT        NOT NULL DEFAULT 'USD',
    method        TEXT        NOT NULL,
    notes         TEXT,
    receipt_url   TEXT,
    is_reversed   BOOLEAN     NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON fee_obligations (team_id, period_year, period_month);
CREATE INDEX ON fee_obligations (membership_id);
CREATE INDEX ON fee_obligations (status);
CREATE INDEX ON payments (team_id);
CREATE INDEX ON payments (obligation_id);
CREATE INDEX ON payments (payer_id);

-- ─── Competencias ────────────────────────────────────────────────────────────
--
-- Un amistoso y un torneo son la misma cosa con distinta cantidad de equipos y
-- distintas reglas de avance, así que comparten tabla. Lo que cambia —la
-- negociación de fecha de un amistoso, el fixture de un torneo— vive aparte.

CREATE TABLE competitions (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    sport_id            TEXT        NOT NULL,
    type                TEXT        NOT NULL CHECK (type IN ('friendly', 'tournament', 'league')),
    name                TEXT        NOT NULL,
    organizer_team_id   UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    status              TEXT        NOT NULL DEFAULT 'draft'
                                    CHECK (status IN ('draft', 'active', 'finished', 'cancelled')),
    -- El equipo juega contra sí mismo: la pichanga entre los propios. Es un
    -- amistoso sin rival, no un tipo nuevo de competencia; se juega igual, se
    -- convoca igual y se cobra igual. Lo único distinto es de dónde salen los
    -- jugadores, y eso es exactamente lo que dice esta bandera.
    is_internal         BOOLEAN     NOT NULL DEFAULT FALSE,
    start_at            TIMESTAMPTZ,
    end_at              TIMESTAMPTZ,
    -- Desnormalizado a propósito: la lista de competencias del mobile muestra
    -- el lugar en cada tarjeta, y resolverlo desde las propuestas obligaría a
    -- una consulta por competencia solo para pintar una línea de texto.
    venue               TEXT,
    players_per_side    SMALLINT,
    -- En unidades menores de la moneda. El peso chileno no tiene decimales:
    -- tratarlo como centavos infla los totales ×100.
    venue_cost_amount   BIGINT,
    venue_cost_currency TEXT,
    -- Cuota fija por jugador, determinada al crear la competencia. Se guarda en
    -- vez de recalcularse porque es el número que se le prometió al manager en
    -- el asistente: derivarlo en cada lectura deja la puerta abierta a que un
    -- cambio de fórmula altere en silencio lo que ya se le cobró a la gente.
    player_share        BIGINT,
    rules               JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
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

-- Sin CHECK de home <> away: el partido interno tiene al mismo equipo de los dos
-- lados. Que un partido normal no sea contra uno mismo se garantiza donde se
-- crean los partidos, que son dos lugares y solo dos: aceptar un desafío usa los
-- dos equipos del desafío, y ahí el CHECK de friendly_challenges ya lo impide; y
-- crear un partido interno, donde ser el mismo equipo es justamente el punto.
CREATE TABLE matches (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    competition_id UUID        NOT NULL REFERENCES competitions(id) ON DELETE CASCADE,
    home_team_id   UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    away_team_id   UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    scheduled_at   TIMESTAMPTZ NOT NULL,
    venue          TEXT,
    status         TEXT        NOT NULL DEFAULT 'draft'
                               CHECK (status IN ('draft', 'confirmed', 'completed', 'cancelled')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Convocatoria: el manager llama, el jugador responde.
--
-- Es entidad propia y no un campo dentro del partido porque de acá salen el
-- historial de asistencia y las estadísticas por jugador. Guardado como JSON
-- adentro del partido, eso sería inconsultable.
--
-- 'not-called' no se guarda: es la ausencia de fila. Un jugador del plantel sin
-- convocatoria está sin convocar, y así el estado no se puede desincronizar
-- del plantel real.
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

-- ─── Plata ───────────────────────────────────────────────────────────────────

-- Cargos: plata que un miembro le debe al equipo.
--
-- Un solo modelo para los dos motivos que existen —la cuota mensual y el costo
-- de cancha de un partido— en vez de dos sistemas de cobro paralelos. Son la
-- misma forma: un monto contra un miembro, por un motivo, que se paga y se
-- confirma. Separarlos obliga a mantener dos veces el flujo de comprobantes,
-- reversas y reportes.
CREATE TABLE charges (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id       UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    membership_id UUID        NOT NULL REFERENCES memberships(id) ON DELETE CASCADE,
    -- De dónde sale el cargo. El par (tipo, id) apunta a la cuota o a la
    -- competencia; no es FK porque referencia tablas distintas según el tipo.
    source_type   TEXT        NOT NULL CHECK (source_type IN ('monthly_fee', 'match_cost')),
    source_id     UUID        NOT NULL,
    -- En unidades menores de la moneda. CLP no tiene decimales.
    amount        BIGINT      NOT NULL CHECK (amount >= 0),
    currency      TEXT        NOT NULL,
    -- El comprobante da el cargo por pagado sin revisión de un tercero: el
    -- manager mira su cartola, no la imagen. 'submitted' sigue aceptado porque
    -- el código todavía lo conoce y un rollback del backend vuelve a producirlo.
    status        TEXT        NOT NULL DEFAULT 'pending'
                              CHECK (status IN ('pending', 'submitted', 'paid', 'waived')),
    due_date      DATE,
    receipt_url   TEXT,
    submitted_at  TIMESTAMPTZ,
    confirmed_at  TIMESTAMPTZ,
    -- Un 'paid' sin confirmed_by es un pago declarado por el propio deudor, y
    -- así se distingue de los que sí pasaron por un tesorero.
    confirmed_by  UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Un miembro no puede tener dos cargos por el mismo motivo. Es lo que hace
    -- que rehacer el reparto sea idempotente en vez de duplicar la deuda.
    UNIQUE (source_type, source_id, membership_id)
);

CREATE INDEX ON charges (team_id, status);
CREATE INDEX ON charges (membership_id);
CREATE INDEX ON charges (source_type, source_id);

-- Fondos del equipo: lo que deja cada reparto del costo de cancha.
--
-- Cuando el reparto recauda más que la mitad del lugar (porque fueron más
-- jugadores que la nómina, los cambios), el excedente queda en los fondos del
-- equipo.
--
-- Es un snapshot por origen, no un libro diario: cada vez que se rehace el
-- reparto de ese partido, la fila se reemplaza con el excedente nuevo. Que el
-- monto pueda ser negativo no es un bug: si fueron menos que la nómina, el
-- equipo absorbe la diferencia entre lo recaudado y su mitad de la cancha.
CREATE TABLE team_funds (
    team_id     UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    -- Mismo par (tipo, id) que charges.
    source_type TEXT        NOT NULL CHECK (source_type IN ('match_cost')),
    source_id   UUID        NOT NULL,
    amount      BIGINT      NOT NULL,
    currency    TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (team_id, source_type, source_id)
);

CREATE INDEX ON team_funds (team_id);

-- Gastos del equipo: plata que sale.
--
-- El origen es opcional a propósito. Un gasto puede colgar de un partido (el
-- árbitro, las pecheras de ese amistoso) y entonces entra en el balance de ese
-- partido; o no colgar de nada (pelotas, botiquín, la cuota de la liga) y ser
-- gasto del equipo a secas. Obligar a elegir partido haría que lo segundo se
-- anote colgado de un partido cualquiera, y ahí el balance por partido deja de
-- querer decir algo.
CREATE TABLE expenses (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id      UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    -- Quién lo anotó. Se conserva el gasto si el usuario se borra: la plata
    -- salió igual, y un agujero en el balance es peor que un autor sin nombre.
    recorded_by  UUID        REFERENCES users(id) ON DELETE SET NULL,
    -- En unidades menores de la moneda. CLP no tiene decimales.
    amount       BIGINT      NOT NULL CHECK (amount > 0),
    currency     TEXT        NOT NULL,
    category     TEXT        NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    -- De qué partido salió, si salió de uno. NULL = gasto del equipo.
    -- Las dos columnas van juntas o ninguna: media referencia no apunta a nada.
    source_type  TEXT        CHECK (source_type IN ('match_cost')),
    source_id    UUID,
    -- Cuándo se gastó, que no es cuándo se anotó: alguien carga el lunes lo que
    -- pagó el sábado, y el gasto pertenece al mes del sábado.
    expense_date DATE        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT expenses_source_complete CHECK (
        (source_type IS NULL AND source_id IS NULL) OR
        (source_type IS NOT NULL AND source_id IS NOT NULL)
    )
);

-- La consulta del módulo de finanzas: los gastos de un equipo en un mes.
CREATE INDEX ON expenses (team_id, expense_date DESC);
-- La del balance de un partido.
CREATE INDEX ON expenses (source_type, source_id);
