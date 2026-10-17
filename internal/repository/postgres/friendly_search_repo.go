package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
)

type FriendlySearchRepository struct {
	pool *pgxpool.Pool
}

func NewFriendlySearchRepository(pool *pgxpool.Pool) *FriendlySearchRepository {
	return &FriendlySearchRepository{pool: pool}
}

// searchColumns trae la búsqueda con su cancha y el nombre del equipo: el mapa
// necesita las tres cosas en cada pin, y pedirlas aparte serían dos requests
// por marcador.
const searchColumns = `
	s.id, s.team_id, t.name, s.sport_id, s.players_per_side, s.start_at,
	v.id, v.name, v.lat, v.lng, v.verified, v.created_at, v.updated_at,
	s.venue_cost_amount, s.venue_cost_currency, COALESCE(s.message, ''), s.status,
	COALESCE(s.created_by::text, ''), s.created_at, s.expires_at`

const searchFrom = `
	FROM friendly_searches s
	JOIN teams t ON t.id = s.team_id
	JOIN venues v ON v.id = s.venue_id`

// scanSearch lee las columnas de `searchColumns` más las que se le agreguen al
// final (distancia, pedido propio), que van por `extra`.
func scanSearch(row pgx.Row, extra ...any) (*friendly.Search, error) {
	s := &friendly.Search{}
	var amount *int64
	var currency *string
	dest := []any{
		&s.ID, &s.TeamID, &s.TeamName, &s.SportID, &s.PlayersPerSide, &s.StartAt,
		&s.Venue.ID, &s.Venue.Name, &s.Venue.Lat, &s.Venue.Lng, &s.Venue.Verified,
		&s.Venue.CreatedAt, &s.Venue.UpdatedAt,
		&amount, &currency, &s.Message, &s.Status, &s.CreatedBy, &s.CreatedAt,
		&s.ExpiresAt,
	}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	if amount != nil && currency != nil {
		s.VenueCost = &competition.VenueCost{Amount: *amount, Currency: *currency}
	}
	return s, nil
}

func (r *FriendlySearchRepository) Create(ctx context.Context, s *friendly.Search) error {
	const q = `
		INSERT INTO friendly_searches
			(team_id, created_by, sport_id, players_per_side, start_at, venue_id,
			 venue_cost_amount, venue_cost_currency, message, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at`
	var amount *int64
	var currency *string
	if s.VenueCost != nil {
		amount = &s.VenueCost.Amount
		currency = &s.VenueCost.Currency
	}
	if s.Status == "" {
		s.Status = friendly.SearchOpen
	}
	err := r.pool.QueryRow(ctx, q,
		s.TeamID, nullIfEmpty(s.CreatedBy), s.SportID, s.PlayersPerSide, s.StartAt, s.Venue.ID,
		amount, currency, nullIfEmpty(s.Message), s.Status, s.ExpiresAt,
	).Scan(&s.ID, &s.CreatedAt)
	if err != nil {
		return fmt.Errorf("friendlySearch.Create: %w", err)
	}
	return nil
}

func (r *FriendlySearchRepository) FindByID(ctx context.Context, id string) (*friendly.Search, error) {
	q := `SELECT` + searchColumns + searchFrom + ` WHERE s.id = $1`
	s, err := scanSearch(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, friendly.ErrSearchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("friendlySearch.FindByID: %w", err)
	}
	return s, nil
}

/*
ListNearby filtra en dos pasos: primero un rectángulo en grados, que es barato y
usa el índice de (lat, lng), y después la distancia de verdad sobre lo poco que
sobrevive.

Un grado de latitud son ~111 km en todos lados; uno de longitud se achica con el
coseno de la latitud (en Santiago, ~93 km). El rectángulo sale un poco más
grande que el círculo a propósito: lo que sobra lo corta el haversine.
*/
func (r *FriendlySearchRepository) ListNearby(
	ctx context.Context, lat, lng, radiusKm float64, viewerTeamID string, now time.Time,
) ([]*friendly.Search, error) {
	dLat := radiusKm / 111.0
	dLng := radiusKm / (111.0 * math.Max(math.Cos(lat*math.Pi/180), 0.01))

	q := `SELECT * FROM (
			SELECT` + searchColumns + `,
				` + haversineKm("v", "$1", "$2") + ` AS distance_km,
				COALESCE((
					SELECT fc.id::text FROM friendly_challenges fc
					WHERE fc.search_id = s.id
					  AND fc.challenged_team_id = $8
					  AND fc.status IN ('pending', 'countered')
					LIMIT 1
				), '') AS requested_challenge_id
			` + searchFrom + `
			WHERE s.status = 'open'
			  AND s.expires_at > $3
			  AND v.lat BETWEEN $4 AND $5
			  AND v.lng BETWEEN $6 AND $7
		) nearby
		WHERE distance_km <= $9
		ORDER BY distance_km, start_at`

	var viewer any
	if viewerTeamID != "" {
		viewer = viewerTeamID
	}
	rows, err := r.pool.Query(ctx, q,
		lat, lng, now,
		lat-dLat, lat+dLat, lng-dLng, lng+dLng,
		viewer, radiusKm,
	)
	if err != nil {
		return nil, fmt.Errorf("friendlySearch.ListNearby: %w", err)
	}
	defer rows.Close()

	result, err := collect(rows, func(row pgx.Row) (*friendly.Search, error) {
		var distance float64
		var requested string
		s, err := scanSearch(row, &distance, &requested)
		if err != nil {
			return nil, err
		}
		s.DistanceKm = &distance
		s.RequestedChallengeID = requested
		return s, nil
	})
	if err != nil {
		return nil, fmt.Errorf("friendlySearch.ListNearby: scan: %w", err)
	}
	return result, nil
}

func (r *FriendlySearchRepository) Close(
	ctx context.Context, id string, status friendly.SearchStatus, keepChallengeID string,
) ([]*friendly.Challenge, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("friendlySearch.Close: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE friendly_searches SET status = $2 WHERE id = $1 AND status = 'open'`, id, status)
	if err != nil {
		return nil, fmt.Errorf("friendlySearch.Close: search: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, friendly.ErrSearchClosed
	}

	const declineSiblings = `
		UPDATE friendly_challenges
		SET status = 'declined'
		WHERE search_id = $1
		  AND status IN ('pending', 'countered')
		  AND ($2::uuid IS NULL OR id <> $2::uuid)
		RETURNING id, competition_id, challenger_team_id, challenged_team_id, status,
		          expires_at, created_at, search_id::text`
	rows, err := tx.Query(ctx, declineSiblings, id, nullIfEmpty(keepChallengeID))
	if err != nil {
		return nil, fmt.Errorf("friendlySearch.Close: siblings: %w", err)
	}
	declined, err := collect(rows, func(row pgx.Row) (*friendly.Challenge, error) {
		ch := &friendly.Challenge{}
		err := row.Scan(
			&ch.ID, &ch.CompetitionID, &ch.ChallengerTeamID, &ch.ChallengedTeamID, &ch.Status,
			&ch.ExpiresAt, &ch.CreatedAt, &ch.SearchID,
		)
		return ch, err
	})
	if err != nil {
		return nil, fmt.Errorf("friendlySearch.Close: siblings scan: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("friendlySearch.Close: commit: %w", err)
	}
	return declined, nil
}

func (r *FriendlySearchRepository) ExpireStale(ctx context.Context, now time.Time) error {
	const q = `UPDATE friendly_searches SET status = 'expired' WHERE status = 'open' AND expires_at <= $1`
	if _, err := r.pool.Exec(ctx, q, now); err != nil {
		return fmt.Errorf("friendlySearch.ExpireStale: %w", err)
	}
	return nil
}
