-- El comprobante da el cargo por pagado, sin revisión de un tercero.
--
-- El estado 'submitted' existía para el control de dos ojos: el jugador subía
-- el comprobante y el manager confirmaba. En la práctica el manager mira su
-- cartola, no la imagen —que además ni siquiera se guarda—, así que el paso no
-- verificaba nada y dejaba cobros semanas en el limbo. Se pasa a confiar en
-- quien declara haber transferido.
--
-- Esto migra los que quedaron a mitad de camino: ya subieron su comprobante y
-- bajo la regla nueva eso alcanza. Sin esto quedarían atascados para siempre,
-- porque la app ya no ofrece confirmarlos.
--
-- `confirmed_by` se deja como está —NULL en todos estos— porque es cierto que
-- nadie los revisó. Un 'paid' sin `confirmed_by` es un pago declarado por el
-- deudor, y así se distingue de los que sí pasaron por un tesorero.
UPDATE charges
SET status = 'paid',
    confirmed_at = COALESCE(confirmed_at, submitted_at, NOW())
WHERE status = 'submitted';

-- El CHECK sigue aceptando 'submitted': el valor no se retira del esquema para
-- que un rollback del backend —que vuelve a producirlo— no choque contra la
-- restricción.
