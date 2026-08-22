-- Vuelve al estado anterior: un token por cuenta y ningún historial.
--
-- La columna se rearma antes de tirar la tabla, porque si no todo el mundo
-- queda sin push hasta volver a abrir la app. De los tokens de una persona se
-- elige el último visto, que es el teléfono donde estuvo más recientemente —lo
-- más parecido a lo que la columna habría tenido—.
ALTER TABLE users ADD COLUMN push_token TEXT;

UPDATE users u
SET push_token = t.token
FROM (
    SELECT DISTINCT ON (user_id) user_id, token
    FROM push_tokens
    ORDER BY user_id, last_seen_at DESC
) AS t
WHERE t.user_id = u.id;

DROP TABLE IF EXISTS push_tokens;

-- El historial se pierde entero y no hay de dónde reconstruirlo: son eventos
-- que no quedaban anotados en ningún otro lado. Lo que sigue funcionando al
-- bajar es el push en vivo —los tres `NotifyAsync` originales— y la pantalla de
-- novedades vuelve a mostrar solo invitaciones a equipo.
DROP TABLE IF EXISTS notifications;
