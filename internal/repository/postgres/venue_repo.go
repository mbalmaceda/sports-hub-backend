package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/venue"
)

type VenueRepository struct {
	pool *pgxpool.Pool
}

func NewVenueRepository(pool *pgxpool.Pool) *VenueRepository {
	return &VenueRepository{pool: pool}
}

/*
haversineKm es la distancia en km entre la cancha `v` y el punto ($lat, $lng),
como expresión SQL.

Va escrita a mano y no con PostGIS o earthdistance porque ninguna de las dos
está habilitada, y para radios de 10 km la diferencia contra una geodesia de
verdad son metros. Los placeholders se pasan para que la misma fórmula sirva en
consultas con distinta numeración de parámetros.
*/
func haversineKm(alias, latParam, lngParam string) string {
	return fmt.Sprintf(`(6371 * 2 * asin(sqrt(
		power(sin(radians(%[1]s.lat - %[2]s) / 2), 2) +
		cos(radians(%[2]s)) * cos(radians(%[1]s.lat)) *
		power(sin(radians(%[1]s.lng - %[3]s) / 2), 2)
	)))`, alias, latParam, lngParam)
}

const venueColumns = `v.id, v.name, v.lat, v.lng, v.verified, v.created_by, v.created_at, v.updated_at, v.rejected_at`

// scanVenue lee `venueColumns` más lo que se agregue al final, por `extra`.
func scanVenue(row pgx.Row, extra ...any) (*venue.Venue, error) {
	v := &venue.Venue{}
	dest := []any{&v.ID, &v.Name, &v.Lat, &v.Lng, &v.Verified, &v.CreatedBy, &v.CreatedAt, &v.UpdatedAt, &v.RejectedAt}
	err := row.Scan(append(dest, extra...)...)
	return v, err
}

func scanVenueWithDistance(row pgx.Row) (*venue.Venue, error) {
	var distance *float64
	v, err := scanVenue(row, &distance)
	v.DistanceKm = distance
	return v, err
}

/*
alive es el filtro de lo que sigue en el catálogo. Lo usan el buscador y la
deduplicación: una cancha rechazada no se ofrece, y si alguien vuelve a
proponer el mismo lugar nace una pendiente nueva —vuelve a la cola— en vez de
revivir la rechazada sin que nadie la mire.
*/
const alive = `v.rejected_at IS NULL`

func (r *VenueRepository) FindByID(ctx context.Context, id string) (*venue.Venue, error) {
	q := `SELECT ` + venueColumns + ` FROM venues v WHERE v.id = $1`
	v, err := scanVenue(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, venue.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("venue.FindByID: %w", err)
	}
	return v, nil
}

// likePattern escapa los comodines de LIKE: alguien que busca "50%" no está
// pidiendo todas las canchas que empiezan con 50.
func likePattern(key string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(key)
	return "%" + escaped + "%"
}

func (r *VenueRepository) Search(
	ctx context.Context, query string, lat, lng *float64, limit int,
) ([]*venue.Venue, error) {
	pattern := likePattern(venue.NameKey(query))

	var rows pgx.Rows
	var err error
	if lat != nil && lng != nil {
		q := `SELECT ` + venueColumns + `, ` + haversineKm("v", "$2", "$3") + ` AS distance_km
			FROM venues v
			WHERE v.name_key LIKE $1 AND ` + alive + `
			ORDER BY v.verified DESC, distance_km
			LIMIT $4`
		rows, err = r.pool.Query(ctx, q, pattern, *lat, *lng, limit)
	} else {
		q := `SELECT ` + venueColumns + `, NULL::double precision AS distance_km
			FROM venues v
			WHERE v.name_key LIKE $1 AND ` + alive + `
			ORDER BY v.verified DESC, v.name
			LIMIT $2`
		rows, err = r.pool.Query(ctx, q, pattern, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("venue.Search: %w", err)
	}
	defer rows.Close()

	result, err := collect(rows, scanVenueWithDistance)
	if err != nil {
		return nil, fmt.Errorf("venue.Search: scan: %w", err)
	}
	return result, nil
}

/*
FindOrCreate busca la misma cancha antes de insertar.

No es a prueba de carreras —dos managers sugiriendo el mismo complejo en el
mismo milisegundo dejan dos filas— y está bien así: el índice que lo impediría
tendría que ser geográfico, y un duplicado ocasional se resuelve con `Merge`
desde el admin. Lo que sí evita es el caso de todos los días: el doble toque, y
el segundo manager que escribe el mismo nombre una semana después.
*/
func (r *VenueRepository) FindOrCreate(ctx context.Context, v *venue.Venue) (*venue.Venue, bool, error) {
	key := venue.NameKey(v.Name)

	q := `SELECT ` + venueColumns + `
		FROM venues v
		WHERE v.name_key = $1 AND ` + alive + ` AND ` + haversineKm("v", "$2", "$3") + ` <= $4
		ORDER BY v.verified DESC, ` + haversineKm("v", "$2", "$3") + `
		LIMIT 1`
	existing, err := scanVenue(r.pool.QueryRow(ctx, q, key, v.Lat, v.Lng, venue.DedupeRadiusKm))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("venue.FindOrCreate: lookup: %w", err)
	}

	if err := r.Create(ctx, v); err != nil {
		return nil, false, err
	}
	return v, true, nil
}

/*
List es la cola del admin: cada cancha con cuánto se usa y quién la propuso.

Primero las pendientes, después las verificadas y al final las rechazadas; dentro
de cada grupo, las más nuevas arriba. Los conteos van en subconsultas y no en un
JOIN con GROUP BY porque son dos tablas independientes: juntarlas en un JOIN
multiplicaría una por la otra.
*/
func (r *VenueRepository) List(ctx context.Context, f venue.Filter) ([]*venue.AdminVenue, error) {
	q := `SELECT ` + venueColumns + `,
			(SELECT COUNT(*) FROM friendly_searches s
			 WHERE s.venue_id = v.id AND s.status = 'open' AND s.expires_at > now()) AS open_searches,
			(SELECT COUNT(*) FROM competitions c WHERE c.venue_id = v.id) AS competitions,
			COALESCE(u.name, '') AS suggested_by
		FROM venues v
		LEFT JOIN users u ON u.id = v.created_by
		WHERE v.name_key LIKE $1
		  AND ($2 = ''
		    OR ($2 = 'pending'  AND NOT v.verified AND v.rejected_at IS NULL)
		    OR ($2 = 'verified' AND v.verified AND v.rejected_at IS NULL)
		    OR ($2 = 'rejected' AND v.rejected_at IS NOT NULL))
		ORDER BY (v.rejected_at IS NOT NULL), v.verified, v.created_at DESC`
	rows, err := r.pool.Query(ctx, q, likePattern(venue.NameKey(f.Query)), string(f.Status))
	if err != nil {
		return nil, fmt.Errorf("venue.List: %w", err)
	}
	defer rows.Close()

	result, err := collect(rows, func(row pgx.Row) (*venue.AdminVenue, error) {
		var searches, competitions int
		var suggestedBy string
		v, err := scanVenue(row, &searches, &competitions, &suggestedBy)
		if err != nil {
			return nil, err
		}
		return &venue.AdminVenue{
			Venue: *v, OpenSearches: searches, Competitions: competitions, SuggestedBy: suggestedBy,
		}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("venue.List: scan: %w", err)
	}
	return result, nil
}

// Nearby mide desde la cancha `id` sin pedirle la coordenada a quien llama:
// así no hay forma de pedir candidatas para un punto que no es el de la cancha.
func (r *VenueRepository) Nearby(ctx context.Context, id string, radiusKm float64) ([]*venue.Venue, error) {
	q := `SELECT ` + venueColumns + `, ` + haversineKm("v", "o.lat", "o.lng") + ` AS distance_km
		FROM venues v, (SELECT lat, lng FROM venues WHERE id = $1) o
		WHERE v.id <> $1 AND ` + alive + `
		  AND ` + haversineKm("v", "o.lat", "o.lng") + ` <= $2
		ORDER BY v.verified DESC, distance_km
		LIMIT 10`
	rows, err := r.pool.Query(ctx, q, id, radiusKm)
	if err != nil {
		return nil, fmt.Errorf("venue.Nearby: %w", err)
	}
	defer rows.Close()

	result, err := collect(rows, scanVenueWithDistance)
	if err != nil {
		return nil, fmt.Errorf("venue.Nearby: scan: %w", err)
	}
	return result, nil
}

func (r *VenueRepository) Create(ctx context.Context, v *venue.Venue) error {
	const q = `
		INSERT INTO venues (name, name_key, lat, lng, verified, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`
	v.Name = strings.TrimSpace(v.Name)
	err := r.pool.QueryRow(ctx, q,
		v.Name, venue.NameKey(v.Name), v.Lat, v.Lng, v.Verified, v.CreatedBy,
	).Scan(&v.ID, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return fmt.Errorf("venue.Create: %w", err)
	}
	return nil
}

func (r *VenueRepository) Update(ctx context.Context, id string, u venue.Update) (*venue.Venue, error) {
	var name, key *string
	if u.Name != nil {
		trimmed := strings.TrimSpace(*u.Name)
		k := venue.NameKey(trimmed)
		name, key = &trimmed, &k
	}
	const q = `
		UPDATE venues v SET
			name       = COALESCE($2, v.name),
			name_key   = COALESCE($3, v.name_key),
			lat        = COALESCE($4, v.lat),
			lng        = COALESCE($5, v.lng),
			-- Rechazar le quita la verificación; aprobar la saca de rechazadas.
			verified   = CASE WHEN $7::boolean IS TRUE THEN false ELSE COALESCE($6, v.verified) END,
			rejected_at = CASE
				WHEN $7::boolean IS TRUE THEN COALESCE(v.rejected_at, now())
				WHEN $7::boolean IS FALSE OR $6::boolean IS TRUE THEN NULL
				ELSE v.rejected_at
			END,
			updated_at = now()
		WHERE v.id = $1
		RETURNING ` + venueColumns
	v, err := scanVenue(r.pool.QueryRow(ctx, q, id, name, key, u.Lat, u.Lng, u.Verified, u.Rejected))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, venue.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("venue.Update: %w", err)
	}
	return v, nil
}

/*
Merge reasigna y borra en una transacción.

`competitions.venue` (el texto) no se toca: es el nombre con que se jugó ese
partido, y lo que se corrige al fusionar es a qué punto del mapa apunta, no la
historia.
*/
func (r *VenueRepository) Merge(ctx context.Context, fromID, intoID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("venue.Merge: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM venues WHERE id = $1)`, intoID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("venue.Merge: check target: %w", err)
	}
	if !exists {
		return venue.ErrNotFound
	}

	if _, err := tx.Exec(ctx, `UPDATE competitions SET venue_id = $2 WHERE venue_id = $1`, fromID, intoID); err != nil {
		return fmt.Errorf("venue.Merge: competitions: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE friendly_searches SET venue_id = $2 WHERE venue_id = $1`, fromID, intoID); err != nil {
		return fmt.Errorf("venue.Merge: searches: %w", err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM venues WHERE id = $1`, fromID)
	if err != nil {
		return fmt.Errorf("venue.Merge: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return venue.ErrNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("venue.Merge: commit: %w", err)
	}
	return nil
}
