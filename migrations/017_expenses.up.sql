-- Gastos del equipo: plata que sale.
--
-- Es la otra mitad de las finanzas. Hasta acá el backend solo sabía de plata que
-- entra —cuotas, cargos, fondos— y los gastos vivían en un mock del cliente, que
-- no los compartía entre dispositivos ni sobrevivía a cerrar la app.
--
-- El origen es opcional a propósito. Un gasto puede colgar de un partido (el
-- árbitro, las pecheras de ese amistoso) y entonces entra en el balance de ese
-- partido; o no colgar de nada (pelotas, botiquín, la cuota de la liga) y ser
-- gasto del equipo a secas. Obligar a elegir partido haría que lo segundo se
-- anote colgado de un partido cualquiera, y ahí el balance por partido deja de
-- querer decir algo.
--
-- Mismo par (source_type, source_id) que `charges` y `team_funds`: el balance de
-- un partido cruza las tres tablas por la competencia, así que la forma de
-- apuntar tiene que ser la misma.

CREATE TABLE expenses (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    -- Quién lo anotó. Se conserva el gasto si el usuario se borra: la plata
    -- salió igual, y un agujero en el balance es peor que un autor sin nombre.
    recorded_by UUID        REFERENCES users(id) ON DELETE SET NULL,
    -- En unidades menores de la moneda. CLP no tiene decimales.
    amount      BIGINT      NOT NULL CHECK (amount > 0),
    currency    TEXT        NOT NULL,
    category    TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    -- De qué partido salió, si salió de uno. NULL = gasto del equipo.
    -- Las dos columnas van juntas o ninguna: media referencia no apunta a nada.
    source_type TEXT        CHECK (source_type IN ('match_cost')),
    source_id   UUID,
    -- Cuándo se gastó, que no es cuándo se anotó: alguien carga el lunes lo que
    -- pagó el sábado, y el gasto pertenece al mes del sábado.
    expense_date DATE       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT expenses_source_complete CHECK (
        (source_type IS NULL AND source_id IS NULL) OR
        (source_type IS NOT NULL AND source_id IS NOT NULL)
    )
);

-- La consulta del módulo de finanzas: los gastos de un equipo en un mes.
CREATE INDEX ON expenses (team_id, expense_date DESC);
-- La del balance de un partido.
CREATE INDEX ON expenses (source_type, source_id);
