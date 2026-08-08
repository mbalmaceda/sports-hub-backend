-- El que organiza el equipo muchas veces también juega. Hasta acá el rol decidía
-- las dos cosas a la vez: ser manager implicaba quedar fuera de la plantilla, de
-- las convocatorias y de las cuotas, porque la app filtraba por `role <> 'manager'`.
--
-- Se separan los dos conceptos: `role` es qué puede hacer la persona con el
-- equipo (administrar, cobrar), y `plays_as_player` si ocupa un lugar en el
-- plantel. Un manager que juega tiene role='manager' y plays_as_player=true.
ALTER TABLE memberships ADD COLUMN plays_as_player BOOLEAN NOT NULL DEFAULT TRUE;

-- Backfill con el mismo criterio que aplicaba el código, para que ninguna
-- plantilla ni cuota se mueva de lugar al aplicar esto. Los managers que además
-- juegan se marcan desde la app.
UPDATE memberships SET plays_as_player = (role <> 'manager');
