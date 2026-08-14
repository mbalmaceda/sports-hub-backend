-- OJO: revertir esto no borra a los invitados, los asciende. Las membresías con
-- kind='guest' quedan indistinguibles de las del plantel, y a partir de la
-- barrida siguiente les toca cuota mensual como a cualquiera. Si hubo parches
-- en producción, hay que darlos de baja antes de bajar esta migración.
DROP TABLE IF EXISTS match_guest_invites;

ALTER TABLE memberships DROP COLUMN IF EXISTS kind;
