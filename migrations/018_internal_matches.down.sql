-- OJO: volver atrás falla si ya hay partidos internos guardados, y está bien
-- que falle. Restaurar el CHECK con esas filas adentro es pedirle a Postgres que
-- valide algo que los datos no cumplen; borrarlas para que pase sería tirar
-- partidos que alguien organizó. Si hace falta bajar de verdad, primero se
-- decide qué pasa con esos partidos.
ALTER TABLE matches
    ADD CONSTRAINT matches_check CHECK (home_team_id <> away_team_id);

ALTER TABLE competitions
    DROP COLUMN IF EXISTS is_internal;
