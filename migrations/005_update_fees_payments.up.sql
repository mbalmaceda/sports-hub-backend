-- Rediseño completo de fee_obligations y payments para matchear los tipos del mobile.
-- No hay data productiva todavía, se pueden reemplazar sin riesgo.

DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS fee_obligations;

CREATE TABLE fee_obligations (
    id            UUID     PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id       UUID     NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    membership_id UUID     NOT NULL REFERENCES memberships(id) ON DELETE CASCADE,
    period_year   SMALLINT NOT NULL,
    period_month  SMALLINT NOT NULL CHECK (period_month BETWEEN 1 AND 12),
    amount        BIGINT   NOT NULL,
    currency      TEXT     NOT NULL DEFAULT 'USD',
    due_date      DATE     NOT NULL,
    status        TEXT     NOT NULL DEFAULT 'pending',
    paid_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (membership_id, period_year, period_month)
);

CREATE TABLE payments (
    id            UUID  PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id       UUID  NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    obligation_id UUID  REFERENCES fee_obligations(id) ON DELETE SET NULL,
    payer_id      UUID  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recorded_by   UUID  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount        BIGINT NOT NULL,
    currency      TEXT   NOT NULL DEFAULT 'USD',
    method        TEXT   NOT NULL,
    notes         TEXT,
    receipt_url   TEXT,
    is_reversed   BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON fee_obligations (team_id, period_year, period_month);
CREATE INDEX ON fee_obligations (membership_id);
CREATE INDEX ON fee_obligations (status);
CREATE INDEX ON payments (team_id);
CREATE INDEX ON payments (obligation_id);
CREATE INDEX ON payments (payer_id);
