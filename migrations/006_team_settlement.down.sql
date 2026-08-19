-- Se va la tabla y con ella las deudas entre equipos. No hay dónde
-- reconstruirlas: son datos nuevos que antes no existían en ningún lado.
--
-- Ojo con lo que queda al bajar: los cobros a los jugadores de los dos equipos
-- siguen intactos, así que el rival vuelve a quedarse con su mitad y el
-- organizador vuelve a comerse la cancha entera. Es exactamente el estado
-- anterior a esta migración.
DROP TABLE IF EXISTS team_settlements;
