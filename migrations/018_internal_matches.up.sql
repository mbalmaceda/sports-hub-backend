-- Partido interno: el equipo juega contra sí mismo.
--
-- Hasta acá una competencia amistosa siempre necesitaba un rival: se creaba el
-- desafío, el otro equipo aceptaba y recién ahí nacía el partido. Eso deja
-- afuera el caso más común de todos —el equipo grande que junta catorce y arma
-- la pichanga entre ellos—, que no tiene rival a quien desafiar ni nada que
-- negociar: el partido existe desde el momento en que se crea.
--
-- Se modela como lo que es, un amistoso sin rival, y no como un tipo nuevo de
-- competencia: se juega igual, se convoca igual y se cobra igual. Lo único
-- distinto es de dónde salen los jugadores, y eso es exactamente lo que dice
-- esta bandera.
ALTER TABLE competitions
    ADD COLUMN is_internal BOOLEAN NOT NULL DEFAULT FALSE;

-- El partido interno tiene al mismo equipo de los dos lados, así que el CHECK
-- que lo prohibía se cae.
--
-- Lo que garantizaba —que un partido normal no sea contra uno mismo— pasa a
-- estar donde se crean los partidos, que son dos lugares y solo dos: aceptar un
-- desafío usa los dos equipos del desafío, y ahí el CHECK de
-- `friendly_challenges` ya impide que sean el mismo; y crear un partido interno,
-- donde ser el mismo equipo es justamente el punto.
--
-- La alternativa era dejar `away_team_id` en NULL, pero eso obliga a todo lo que
-- lee un partido a preguntarse si hay rival, para no ganar nada: en un partido
-- interno los dos lados existen y los dos son nuestros.
--
-- Se busca por definición y no por nombre: el CHECK se declaró sin nombre al
-- crear la tabla, así que se llama como Postgres haya decidido. Un
-- `DROP CONSTRAINT IF EXISTS` con el nombre adivinado no falla si le erramos
-- —no hace nada—, y el error aparecería recién al insertar el primer partido
-- interno, en producción.
DO $$
DECLARE
    self_match_check TEXT;
BEGIN
    SELECT con.conname INTO self_match_check
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid = con.conrelid
    WHERE rel.relname = 'matches'
      AND con.contype = 'c'
      AND pg_get_constraintdef(con.oid) LIKE '%home_team_id <> away_team_id%';

    IF self_match_check IS NOT NULL THEN
        EXECUTE format('ALTER TABLE matches DROP CONSTRAINT %I', self_match_check);
    END IF;
END $$;
