-- Incorporación de jugadores a un equipo.
--
-- Hay dos caminos y son simétricos pero distintos, así que se modelan aparte:
--
--   team_invitations → el equipo busca a la persona y la invita. Acepta la persona.
--   join_requests    → la persona encuentra al equipo y pide entrar. Acepta el equipo.
--
-- Colapsarlos en una tabla con un campo "dirección" obliga a preguntar quién
-- puede aceptar en cada consulta, y ese es justo el chequeo que no hay que
-- equivocar: si se invierte, cualquiera se mete solo a cualquier equipo.

-- Identificación tributaria: RUT en Chile, CUIT en Argentina, CPF en Brasil.
-- Se llama tax_id y no rut porque es el campo con el que un manager busca a un
-- jugador para invitarlo, y ese buscador tiene que seguir sirviendo cuando la
-- app cruce la cordillera.
ALTER TABLE users ADD COLUMN tax_id TEXT;

-- Único pero admitiendo NULL: no todos cargan el dato, pero dos personas no
-- pueden compartir el mismo. El índice además hace rápida la búsqueda exacta,
-- que es la única que se permite.
CREATE UNIQUE INDEX users_tax_id_unique ON users (tax_id) WHERE tax_id IS NOT NULL;

CREATE TABLE team_invitations (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id           UUID        NOT NULL REFERENCES teams(id)  ON DELETE CASCADE,
    invited_by_user_id UUID       NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    user_id           UUID        NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    status            TEXT        NOT NULL DEFAULT 'sent'
                                  CHECK (status IN ('sent', 'accepted', 'declined', 'expired', 'revoked')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at      TIMESTAMPTZ
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
