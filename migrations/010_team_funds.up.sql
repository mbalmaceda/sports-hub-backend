-- Fondos del equipo: lo que deja cada reparto del costo de cancha.
--
-- Cuando el reparto recauda más que la mitad del lugar (porque fueron más
-- jugadores que la nómina, los cambios), el excedente queda en los fondos del
-- equipo. Es el contrapeso de los gastos: plata que el equipo tiene, no que
-- debe.
--
-- Es un snapshot por origen, no un libro diario: cada vez que se rehace el
-- reparto de ese partido, la fila se reemplaza con el excedente nuevo. Que el
-- monto pueda ser negativo no es un bug: si fueron menos que la nómina, el
-- equipo absorbe la diferencia entre lo recaudado y su mitad de la cancha.

CREATE TABLE team_funds (
    team_id     UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    -- De qué reparto salió la plata. Mismo par (tipo, id) que charges.
    source_type TEXT        NOT NULL CHECK (source_type IN ('match_cost')),
    source_id   UUID        NOT NULL,
    amount      BIGINT      NOT NULL,
    currency    TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (team_id, source_type, source_id)
);

CREATE INDEX ON team_funds (team_id);
