-- Al volver atrás, quien administre vuelve a quedar fuera de la plantilla por
-- ser manager: es la regla que había antes de la columna.
ALTER TABLE memberships DROP COLUMN plays_as_player;
