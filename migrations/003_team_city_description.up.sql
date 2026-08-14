-- Ciudad y descripción del equipo.
--
-- Las dos salen del alta de equipo del prototipo y son puro dato de exhibición:
-- nadie filtra ni decide nada con ellas todavía. Por eso van como TEXT NOT NULL
-- DEFAULT '' y no como columnas nulables — un equipo sin ciudad tiene ciudad
-- vacía, no ciudad desconocida, y así ningún SELECT tiene que hacer COALESCE.
--
-- La ciudad es texto libre a propósito: una tabla de comunas serviría para
-- Chile y habría que migrarla en el primer equipo argentino.
ALTER TABLE teams
    ADD COLUMN city        TEXT NOT NULL DEFAULT '',
    ADD COLUMN description TEXT NOT NULL DEFAULT '';
