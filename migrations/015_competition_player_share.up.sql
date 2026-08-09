-- Cuota fija por jugador, guardada al crear la competencia.
--
-- Sale de dividir el costo del lugar entre los que entran en cancha por los dos
-- lados, y queda determinada en ese momento: el costo y la nómina no se editan
-- después (la única UPDATE sobre `competitions` toca `status`), así que el valor
-- no puede quedar viejo.
--
-- Se guarda en vez de recalcularse en cada lectura porque es el número que se le
-- prometió al manager en el asistente de creación. Derivarlo cada vez deja la
-- puerta abierta a que un cambio de fórmula altere en silencio lo que ya se le
-- cobró a la gente.
ALTER TABLE competitions ADD COLUMN player_share BIGINT;

-- Las que ya existen se completan con la misma cuenta que venía haciendo el
-- código: división hacia arriba, para que la suma de las partes nunca quede
-- corta contra el costo del lugar.
UPDATE competitions
SET player_share = CEIL(venue_cost_amount::numeric / (players_per_side * 2))::bigint
WHERE venue_cost_amount IS NOT NULL
  AND venue_cost_amount > 0
  AND players_per_side IS NOT NULL
  AND players_per_side > 0;
