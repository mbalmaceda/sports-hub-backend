-- La clave que hace que un aviso periódico se mande una sola vez.
--
-- Las quince notificaciones que ya existían nacen de una acción: alguien
-- desafía, alguien convoca, alguien paga. Pasan una vez y por eso se emiten una
-- vez, sin que nadie tenga que cuidar nada.
--
-- Las que vienen del reloj no funcionan así. Un trabajo que corre cada quince
-- minutos y pregunta "¿qué partidos son mañana?" encuentra el mismo partido en
-- los noventa y seis ticks del día, y sin esta columna mandaría noventa y seis
-- recordatorios. Peor: en Fly la máquina se reinicia y el ticker arranca de
-- cero, así que tampoco alcanza con recordar en memoria lo ya enviado.
--
-- `dedupe_key` invierte el problema: **el horario deja de decidir qué se manda**
-- y pasa a decidir solo cuándo se revisa. Lo que se manda o no lo decide esta
-- clave, que vive en Postgres y sobrevive a cualquier reinicio. Con eso el
-- intervalo del ticker pasa a ser una cuestión de latencia y no de corrección:
-- correr más seguido avisa antes, nunca avisa de más.
ALTER TABLE notifications ADD COLUMN dedupe_key TEXT;

/*
Por qué es una columna aparte y no se reusa `entity_id`.

Son dos preguntas distintas y hay un caso donde las respuestas difieren. El
aviso de cuota vencida apunta a la ficha del jugador —`entity_id` es su
membresía, que es a donde el manager quiere ir— pero tiene que mandarse una vez
**por mes**, así que su clave es la obligación de ese período. Con una sola
columna habría que elegir: o el aviso lleva a ningún lado, o se manda una vez y
nunca más en la vida del jugador.

Además el resumen mensual no apunta a ninguna entidad y aun así necesita clave
(equipo + mes), y `entity_id` es UUID: no hay forma de escribir ahí "este equipo,
julio de 2026".
*/

-- El índice viejo era por (destinatario, tipo, entidad) y solo para los tipos
-- que se reescriben, con la lista de tipos escrita adentro. La clave la
-- generaliza: agregar un aviso deduplicado deja de necesitar una migración.
DROP INDEX IF EXISTS notifications_collapsed_key;

-- Lo que ya estaba colapsando conserva su comportamiento. El índice viejo
-- garantizaba unicidad sobre esas mismas filas, así que el backfill no puede
-- chocar contra el índice nuevo.
UPDATE notifications
SET dedupe_key = 'callup_responses:' || entity_id
WHERE type = 'callup_responses' AND entity_id IS NOT NULL;

-- Parcial porque la mayoría de las notificaciones NO deduplican: dos cobros
-- distintos al mismo jugador son dos avisos, y así tiene que ser.
CREATE UNIQUE INDEX notifications_dedupe_key
    ON notifications (recipient_user_id, dedupe_key)
    WHERE dedupe_key IS NOT NULL;
