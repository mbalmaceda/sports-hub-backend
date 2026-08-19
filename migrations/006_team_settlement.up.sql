-- Lo que un equipo le debe a otro por el lugar donde jugaron.
--
-- El costo de la cancha se reparte entre los que entran por los dos lados, y
-- cada equipo cobra a los suyos. Pero la cancha la paga **uno solo**: el que
-- organizó el partido, que es el que la reservó. Hasta acá esa mitad ajena se
-- perdía en el camino —el rival le cobraba a sus jugadores y la plata se
-- quedaba en su cuenta—, y el organizador terminaba el amistoso $14.000 abajo
-- con el balance del partido diciendo exactamente eso.
--
-- Esta tabla es esa mitad: una deuda del equipo retado con el organizador, que
-- se salda con una transferencia entre managers.
--
-- Es tabla propia y no una fila más en `charges` por una razón concreta: el
-- deudor acá es un **equipo**, no una membresía, y `charges.membership_id` es
-- NOT NULL porque todo lo que cuelga de él —el reparto, el historial del
-- jugador, la pantalla de pago— asume una persona. Y sobre todo:
-- `CreateForSource` reemplaza los cargos pendientes de un origen cada vez que
-- el manager rehace el reparto. Una deuda entre equipos ahí se borraría sola.
CREATE TABLE team_settlements (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Mismo par (tipo, id) que charges, team_funds y expenses. Para
    -- 'match_cost' el id es el de la **competencia**, igual que en charges:
    -- el costo del lugar es de la competencia y no de cada partido.
    --
    -- Por eso el UNIQUE de abajo alcanza hoy: un amistoso es una competencia
    -- con un partido. Un torneo con fixture y canchas distintas por fecha
    -- necesitaría bajar el origen al partido, y ese es el día de revisar esto.
    source_type  TEXT        NOT NULL CHECK (source_type IN ('match_cost')),
    source_id    UUID        NOT NULL,
    -- Quién debe: el equipo retado. Quién cobra: el que organizó y pagó.
    from_team_id UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    to_team_id   UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    -- En unidades menores de la moneda. CLP no tiene decimales.
    amount       BIGINT      NOT NULL CHECK (amount > 0),
    currency     TEXT        NOT NULL,
    -- Dos estados y nada más. No hay 'submitted' esperando revisión: el que
    -- transfiere declara y se le cree, igual que en charges desde que el
    -- comprobante cierra el cobro solo.
    status       TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'paid')),
    paid_at      TIMESTAMPTZ,
    -- Quién declaró la transferencia. Es una persona diciendo "ya la hice", no
    -- un hecho verificado, y por eso tiene que quedar con nombre.
    paid_by      UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Un equipo no se debe a sí mismo. Es lo que deja afuera al partido
    -- interno, donde los dos lados son el mismo club y no hay nada que
    -- transferir.
    CONSTRAINT team_settlements_two_teams CHECK (from_team_id <> to_team_id),
    -- Una sola deuda por partido. Es lo que hace que aceptar el amistoso dos
    -- veces —un reintento, un doble toque— no genere dos.
    UNIQUE (source_type, source_id)
);

-- La consulta del inicio del deudor: "¿debo algo?".
CREATE INDEX ON team_settlements (from_team_id, status);
-- La del organizador: "¿quién me debe?".
CREATE INDEX ON team_settlements (to_team_id, status);

-- Los amistosos que ya estaban acordados cuando llegó esta migración.
--
-- Sin esto, la deuda solo existe para los partidos que se acepten de ahora en
-- adelante: todo lo que ya está agendado queda sin ella y el organizador sigue
-- comiéndose la cancha entera, que es justo lo que esto vino a arreglar.
--
-- **Solo lo que todavía no se jugó.** Un amistoso de hace tres semanas casi
-- seguro ya se arregló entre los dos managers por fuera de la app —en efectivo,
-- por transferencia, o quedó a cuenta del próximo— y aparecerle hoy con un
-- "le debes $14.000 a Racing" es inventarle una deuda que ya no existe. El
-- daño de un cobro de más entre dos clubes que se conocen es peor que el de un
-- balance viejo que quedó incompleto.
--
-- La mitad se calcula igual que en el código: división entera hacia arriba
-- (`(x + 1) / 2` es el `ceilDiv(x, 2)` de charge.TeamShare). Con dos fórmulas
-- distintas quedaría un peso de diferencia entre lo que la app muestra y lo que
-- la deuda dice.
INSERT INTO team_settlements (source_type, source_id, from_team_id, to_team_id, amount, currency)
SELECT 'match_cost',
       c.id,
       f.challenged_team_id,
       f.challenger_team_id,
       (c.venue_cost_amount + 1) / 2,
       c.venue_cost_currency
FROM competitions c
JOIN friendly_challenges f ON f.competition_id = c.id
WHERE f.status = 'accepted'
  AND c.status NOT IN ('cancelled', 'finished')
  -- Todavía no se juega: es la condición que hace segura esta migración.
  AND c.start_at IS NOT NULL
  AND c.start_at > NOW()
  AND c.venue_cost_amount IS NOT NULL
  AND c.venue_cost_amount > 0
  AND c.venue_cost_currency IS NOT NULL
  -- Sin rival no hay a quién transferirle.
  AND c.is_internal = FALSE
  AND f.challenged_team_id <> f.challenger_team_id
ON CONFLICT (source_type, source_id) DO NOTHING;
