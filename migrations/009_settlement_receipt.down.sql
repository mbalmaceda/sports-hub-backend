-- Se pierden los comprobantes declarados. No se pierde ningún pago: lo que
-- cierra la deuda es `status`/`paid_at`, y eso vive en columnas aparte.
ALTER TABLE team_settlements DROP COLUMN receipt_url;
