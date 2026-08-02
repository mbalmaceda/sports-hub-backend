-- Cargos: plata que un miembro le debe al equipo.
--
-- Un solo modelo para los dos motivos que existen —la cuota mensual y el costo
-- de cancha de un partido— en vez de dos sistemas de cobro paralelos. Son la
-- misma forma: un monto contra un miembro, por un motivo, que se paga y se
-- confirma. Separarlos obliga a mantener dos veces el flujo de comprobantes,
-- reversas y reportes.
--
-- `fee_obligations` sigue existiendo: la migración de las cuotas al ledger es
-- un paso aparte, y hacerla en la misma release que estrena la tabla mezclaría
-- un cambio de esquema con una migración de datos productivos.

CREATE TABLE charges (
    id            UUID     PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id       UUID     NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    membership_id UUID     NOT NULL REFERENCES memberships(id) ON DELETE CASCADE,
    -- De dónde sale el cargo. El par (tipo, id) apunta a la cuota o a la
    -- competencia; no es FK porque referencia tablas distintas según el tipo.
    source_type   TEXT     NOT NULL CHECK (source_type IN ('monthly_fee', 'match_cost')),
    source_id     UUID     NOT NULL,
    -- En unidades menores de la moneda. CLP no tiene decimales.
    amount        BIGINT   NOT NULL CHECK (amount >= 0),
    currency      TEXT     NOT NULL,
    -- 'submitted' es el estado que hace falta cuando se paga por transferencia:
    -- el jugador ya transfirió y subió el comprobante, pero nadie lo verificó.
    -- Sin él, o figura moroso habiendo pagado, o se da por pagado sin mirar.
    status        TEXT     NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending', 'submitted', 'paid', 'waived')),
    due_date      DATE,
    receipt_url   TEXT,
    submitted_at  TIMESTAMPTZ,
    confirmed_at  TIMESTAMPTZ,
    confirmed_by  UUID     REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Un miembro no puede tener dos cargos por el mismo motivo. Es lo que hace
    -- que rehacer el reparto sea idempotente en vez de duplicar la deuda.
    UNIQUE (source_type, source_id, membership_id)
);

CREATE INDEX ON charges (team_id, status);
CREATE INDEX ON charges (membership_id);
CREATE INDEX ON charges (source_type, source_id);

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
