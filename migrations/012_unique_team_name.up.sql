-- Los equipos no pueden repetir nombre. El seed lo daba por sentado
-- (`ON CONFLICT DO NOTHING`) y el alta por la app no lo impedía, así que el
-- buscador mostraba duplicados ("Deportivo Norte" x3).
--
-- Se queda, por nombre, el equipo con más miembros (el real, con plantel y
-- cuotas) y se borran los clonados. Todos los FKs a teams son ON DELETE
-- CASCADE, así que la limpieza arrastra bien memberships, cuotas, cobros, etc.
WITH membership_counts AS (
    SELECT team_id, count(*) AS c
    FROM memberships
    GROUP BY team_id
),
ranked AS (
    SELECT t.id, ROW_NUMBER() OVER (
        PARTITION BY lower(t.name)
        ORDER BY COALESCE(mc.c, 0) DESC, t.created_at ASC, t.id
    ) AS rn
    FROM teams t
    LEFT JOIN membership_counts mc ON mc.team_id = t.id
)
DELETE FROM teams
WHERE id IN (SELECT id FROM ranked WHERE rn > 1);

-- Índice único por nombre en minúsculas: dos equipos "Deportivo Norte" y
-- "deportivo norte" serían el mismo para quien busca.
CREATE UNIQUE INDEX teams_name_lower_key ON teams (lower(name));
