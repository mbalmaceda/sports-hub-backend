-- Revierte la marca de borrado. Los datos personales que se anonimizaron al
-- borrar una cuenta no vuelven: eso es a propósito, es el punto del borrado.
ALTER TABLE users DROP COLUMN deleted_at;
