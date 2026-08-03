-- Perfil deportivo del jugador (paso 2 del registro).
-- Todos los campos son opcionales: la cuenta se puede crear sin completarlos.

ALTER TABLE users ADD COLUMN favorite_sport TEXT;
ALTER TABLE users ADD COLUMN height_cm INTEGER CHECK (height_cm > 0);
ALTER TABLE users ADD COLUMN weight_kg NUMERIC(5,1) CHECK (weight_kg > 0);
ALTER TABLE users ADD COLUMN birth_date DATE;
ALTER TABLE users ADD COLUMN alias TEXT;
ALTER TABLE users ADD COLUMN city TEXT;
ALTER TABLE users ADD COLUMN dominant_side TEXT CHECK (dominant_side IN ('right', 'left', 'both'));
ALTER TABLE users ADD COLUMN bio TEXT;
