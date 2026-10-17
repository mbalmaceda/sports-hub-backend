-- Canchas con coordenadas y amistosos publicados sin rival ("buscar rival").
--
-- ── venues ──
--
-- Hasta acá la cancha era texto libre en cada competencia, propuesta y partido.
-- Para poner un punto en un mapa hace falta una coordenada, y pedírsela a cada
-- manager en cada amistoso termina en catorce pines distintos para la misma
-- cancha. Por eso es un catálogo: se carga una vez y se reutiliza.
--
-- El catálogo es curado. Lo que cargamos nosotros nace `verified`; lo que
-- sugiere un manager cuya cancha no estaba nace sin verificar, sirve igual para
-- publicar, y se corrige o se fusiona desde el admin. Así nadie se traba
-- esperando a que alguien cargue su cancha.
--
-- `name_key` es el nombre en minúsculas y sin tildes, armado en Go
-- (`venue.NameKey`). Es lo que se compara al buscar y al deduplicar: "Complejo
-- Maipú" y "complejo maipu" son la misma cancha si están a menos de 300 m.
CREATE TABLE venues (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    name_key    TEXT NOT NULL,
    lat         DOUBLE PRECISION NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lng         DOUBLE PRECISION NOT NULL CHECK (lng BETWEEN -180 AND 180),
    verified    BOOLEAN NOT NULL DEFAULT FALSE,
    -- Nulo para lo que se carga desde el admin, y si se borra la cuenta: la
    -- cancha sigue existiendo aunque quien la sugirió ya no esté.
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX venues_name_key_idx ON venues (name_key);
CREATE INDEX venues_lat_lng_idx ON venues (lat, lng);

-- El texto `venue` se queda: es lo que leen las versiones viejas de la app y
-- todas las pantallas de hoy. `venue_id` es el dato nuevo, y solo existe cuando
-- quien creó la competencia eligió una cancha del catálogo.
ALTER TABLE competitions ADD COLUMN venue_id UUID REFERENCES venues(id) ON DELETE SET NULL;

-- ── friendly_searches ──
--
-- Un amistoso publicado sin rival: fecha, cancha y costo ya resueltos, falta
-- con quién. Cualquier equipo cercano puede pedir jugarlo, y cada pedido es un
-- desafío normal (ver `search_id` abajo). Aceptar uno cierra la búsqueda.
--
-- No es una competencia en borrador a propósito: una competencia tiene un
-- organizador y UN rival, y una búsqueda recibe muchos pedidos. Cada pedido
-- nace con su propia competencia, y la que se acepta es la que queda.
CREATE TABLE friendly_searches (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id             UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    created_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    sport_id            TEXT NOT NULL,
    players_per_side    SMALLINT CHECK (players_per_side BETWEEN 1 AND 30),
    start_at            TIMESTAMPTZ NOT NULL,
    -- La coordenada vive en la cancha y no se copia: si el admin la corrige,
    -- el punto se mueve en todas las búsquedas que la usan.
    venue_id            UUID NOT NULL REFERENCES venues(id),
    venue_cost_amount   BIGINT CHECK (venue_cost_amount >= 0),
    venue_cost_currency TEXT,
    message             TEXT,
    status              TEXT NOT NULL DEFAULT 'open'
                        CHECK (status IN ('open', 'matched', 'cancelled', 'expired')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX friendly_searches_open_idx ON friendly_searches (start_at) WHERE status = 'open';
CREATE INDEX friendly_searches_team_idx ON friendly_searches (team_id);
CREATE INDEX friendly_searches_venue_idx ON friendly_searches (venue_id);

-- Un pedido a una búsqueda es un desafío como cualquier otro, con una
-- diferencia de fondo: el retador es quien PUBLICÓ, no quien tocó el botón.
-- Quien publica reservó la cancha, y en este modelo el que desafía es el que
-- organiza y el que cobra la mitad al rival (ver `ensureVenueSettlement`).
ALTER TABLE friendly_challenges
    ADD COLUMN search_id UUID REFERENCES friendly_searches(id) ON DELETE SET NULL;

-- Un equipo no puede pedir dos veces la misma búsqueda mientras su pedido siga
-- abierto. Rechazado o vencido, puede volver a pedir.
CREATE UNIQUE INDEX friendly_challenges_search_requester_key
    ON friendly_challenges (search_id, challenged_team_id)
    WHERE search_id IS NOT NULL AND status IN ('pending', 'countered');
