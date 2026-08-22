-- Vuelve al índice por tipo y entidad.
--
-- Ojo con lo que queda al bajar: los avisos del trabajo periódico —recordatorio
-- de partido, cuota vencida, resumen mensual— pierden su única defensa contra
-- repetirse. Si el proceso sigue corriendo con esta versión del esquema, manda
-- el mismo recordatorio en cada tick. Bajar esta migración es bajar también el
-- binario que la usa.
DROP INDEX IF EXISTS notifications_dedupe_key;

CREATE UNIQUE INDEX notifications_collapsed_key
    ON notifications (recipient_user_id, type, entity_id)
    WHERE type IN ('callup_responses');

ALTER TABLE notifications DROP COLUMN dedupe_key;
