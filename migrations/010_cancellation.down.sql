-- Ojo: si hay filas en 'cancelled', estos CHECK fallan al crearse. Bajar esta
-- migración exige decidir antes qué hacer con esos cobros —volverlos a
-- 'pending' resucita deudas de partidos que no se jugaron— y por eso no se
-- reasignan solos acá.
ALTER TABLE charges DROP CONSTRAINT charges_status_check;
ALTER TABLE charges ADD CONSTRAINT charges_status_check
    CHECK (status IN ('pending', 'submitted', 'paid', 'waived'));

ALTER TABLE team_settlements DROP CONSTRAINT team_settlements_status_check;
ALTER TABLE team_settlements ADD CONSTRAINT team_settlements_status_check
    CHECK (status IN ('pending', 'paid'));
