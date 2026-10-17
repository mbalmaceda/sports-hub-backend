-- Los cobros borrados no vuelven: eran deuda de gente que no confirmó.
-- La tabla de fondos vuelve vacía; nada la vuelve a escribir.
CREATE TABLE IF NOT EXISTS team_funds (
    team_id     UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    source_type TEXT        NOT NULL CHECK (source_type IN ('match_cost')),
    source_id   UUID        NOT NULL,
    amount      BIGINT      NOT NULL,
    currency    TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (team_id, source_type, source_id)
);

CREATE INDEX IF NOT EXISTS team_funds_team_id_idx ON team_funds (team_id);
