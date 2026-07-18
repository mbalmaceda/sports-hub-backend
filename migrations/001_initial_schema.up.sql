CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE users (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    email      TEXT        NOT NULL UNIQUE,
    role       TEXT        NOT NULL DEFAULT 'player',
    push_token TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE teams (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    sport      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE memberships (
    id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id   UUID        NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    team_id   UUID        NOT NULL REFERENCES teams(id)  ON DELETE CASCADE,
    status    TEXT        NOT NULL DEFAULT 'pending',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, team_id)
);

-- Cuotas que un miembro debe pagar. amount en centavos.
CREATE TABLE fee_obligations (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    membership_id UUID        NOT NULL REFERENCES memberships(id) ON DELETE CASCADE,
    amount        BIGINT      NOT NULL,
    currency      TEXT        NOT NULL DEFAULT 'USD',
    due_date      TIMESTAMPTZ NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'pending',
    description   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE payments (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    fee_obligation_id UUID        NOT NULL REFERENCES fee_obligations(id) ON DELETE CASCADE,
    amount            BIGINT      NOT NULL,
    currency          TEXT        NOT NULL DEFAULT 'USD',
    paid_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    method            TEXT        NOT NULL
);

CREATE INDEX ON memberships (user_id);
CREATE INDEX ON memberships (team_id);
CREATE INDEX ON fee_obligations (membership_id);
CREATE INDEX ON fee_obligations (status);
CREATE INDEX ON payments (fee_obligation_id);
