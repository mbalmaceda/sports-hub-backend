-- Una búsqueda de rival vence a las 24 horas de publicada.
--
-- Hasta acá seguía abierta hasta la hora del partido, que puede caer semanas
-- después: el mapa se llenaba de publicaciones que nadie estaba mirando. El
-- plazo se cuenta desde la creación del registro y nunca pasa del partido, la
-- misma regla que `responseDeadline` aplica a desafíos e invitaciones.
ALTER TABLE friendly_searches ADD COLUMN expires_at TIMESTAMPTZ;

UPDATE friendly_searches
SET expires_at = LEAST(created_at + interval '24 hours', start_at);

ALTER TABLE friendly_searches ALTER COLUMN expires_at SET NOT NULL;

-- El listado y la barrida filtran por el plazo, ya no por la hora del partido.
DROP INDEX IF EXISTS friendly_searches_open_idx;
CREATE INDEX friendly_searches_open_idx ON friendly_searches (expires_at) WHERE status = 'open';
