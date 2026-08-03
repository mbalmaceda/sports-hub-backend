-- Quitar la unicidad de nombre; los equipos duplicados ya eliminados no se
-- restauran.
DROP INDEX IF EXISTS teams_name_lower_key;
