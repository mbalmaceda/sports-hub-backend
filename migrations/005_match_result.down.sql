-- Se van las columnas y con ellas los marcadores cargados. No hay dónde
-- guardarlos: son datos nuevos que antes de esta migración no existían.
ALTER TABLE matches
    DROP CONSTRAINT IF EXISTS matches_score_not_negative,
    DROP CONSTRAINT IF EXISTS matches_score_complete,
    DROP COLUMN IF EXISTS result_recorded_by,
    DROP COLUMN IF EXISTS result_recorded_at,
    DROP COLUMN IF EXISTS away_score,
    DROP COLUMN IF EXISTS home_score;
