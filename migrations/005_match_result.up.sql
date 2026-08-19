-- El marcador: cómo terminó el partido.
--
-- Va en `matches` y no en `competitions` porque el resultado es del encuentro,
-- no del campeonato: un torneo tiene muchos partidos y un solo estado. En el
-- amistoso las dos cosas coinciden —una competencia, un partido— pero poner el
-- marcador arriba obligaría a mudarlo apenas aparezca el fixture de un torneo,
-- que es lo próximo que se viene.
--
-- Esta es la primera columna de lo que después va a ser la métrica del partido
-- (goles por jugador, tarjetas, minutos). Todas cuelgan del partido por el
-- mismo motivo, y por eso el marcador entra como dos columnas tipadas y no como
-- un JSON de "datos del resultado": lo que se consulta —quién ganó, cuántos
-- goles— tiene que poder consultarse.
ALTER TABLE matches
    -- Nulo significa "todavía no lo cargaron", que no es lo mismo que 0 a 0.
    -- Un empate sin goles es un resultado y tiene que poder distinguirse de un
    -- partido que nadie cerró.
    ADD COLUMN home_score         SMALLINT,
    ADD COLUMN away_score         SMALLINT,
    -- Cuándo se cargó, que no es cuándo se jugó. Sirve para saber si el dato es
    -- del rato después del partido o de tres semanas más tarde.
    ADD COLUMN result_recorded_at TIMESTAMPTZ,
    -- Quién lo cargó. El marcador lo declara una persona y nadie lo verifica:
    -- sin el autor, un resultado discutido no tiene a quién preguntarle.
    -- Se conserva el marcador si el usuario se borra, igual que en `expenses`:
    -- el partido terminó como terminó, y un autor sin nombre es mejor que
    -- perder el resultado.
    ADD COLUMN result_recorded_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- Los dos goles van juntos o ninguno: medio marcador no dice nada, y con una
-- sola columna cargada la app no puede decidir si mostrar el resultado.
ALTER TABLE matches
    ADD CONSTRAINT matches_score_complete CHECK (
        (home_score IS NULL AND away_score IS NULL) OR
        (home_score IS NOT NULL AND away_score IS NOT NULL)
    );

-- No hay goles negativos. Es la clase de dato que llega mal desde un teclado
-- numérico, y acá abajo es donde no se puede escapar.
ALTER TABLE matches
    ADD CONSTRAINT matches_score_not_negative CHECK (
        (home_score IS NULL OR home_score >= 0) AND
        (away_score IS NULL OR away_score >= 0)
    );
