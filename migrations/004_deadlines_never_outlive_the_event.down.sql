-- Sin vuelta atrás: el plazo viejo era una fecha calculada al crear la fila y no
-- quedó guardado en ningún lado, así que no hay de dónde reconstruirlo.
--
-- No hace falta que la haya. Lo que esta migración corrige son plazos que
-- prometían tiempo después del partido, y volver a ponerlos sería reintroducir
-- el bug. Bajar el código a la versión anterior deja estas filas coherentes
-- igual: un vencimiento más corto no rompe nada de lo que el código viejo hace.
SELECT 1;
