-- Cancelar un partido que ya movió gente y plata.
--
-- Hasta acá `cancelled` solo existía para una competencia que nunca llegó a ser
-- partido: el desafío que el rival rechazó, o el que venció sin respuesta. Un
-- amistoso acordado, con la nómina citada y media cancha cobrada, no tenía cómo
-- darse de baja — y se cancelan, por lluvia o porque no se juntó la gente.
--
-- Lo que faltaba era dónde dejar los cobros. No alcanza con ninguno de los
-- estados que ya había:
--
--   · `paid` seguiría contando como ingreso del mes, y esa plata está en la
--     cuenta del equipo pero **no es del equipo**: hay que devolverla.
--   · `waived` significa "la cubrió alguien" desde que dejó de ser "exento", así
--     que también suma. Mandar ahí los cargos de un partido cancelado llenaría
--     el mes de plata que nunca entró.
--   · `pending` los dejaría como deuda viva por una cancha que no se usó.
--
-- De ahí `cancelled`: no cuenta ni como ingreso ni como deuda. Y es un cambio de
-- estado y no un DELETE a propósito — la fila conserva el monto, la persona y
-- la fecha de pago, que es la única lista de a quién hay que devolverle. Borrar
-- los cargos dejaría las finanzas en cero y al manager sin saber a quién le
-- debe $2.000.
ALTER TABLE charges DROP CONSTRAINT charges_status_check;
ALTER TABLE charges ADD CONSTRAINT charges_status_check
    CHECK (status IN ('pending', 'submitted', 'paid', 'waived', 'cancelled'));

-- La mitad de la cancha entre equipos, por lo mismo: si el rival ya transfirió,
-- el organizador tiene una plata que devolver, y si no lo hizo, deja de deberla.
ALTER TABLE team_settlements DROP CONSTRAINT team_settlements_status_check;
ALTER TABLE team_settlements ADD CONSTRAINT team_settlements_status_check
    CHECK (status IN ('pending', 'paid', 'cancelled'));
