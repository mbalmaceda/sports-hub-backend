package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
)

type TeamRepository struct {
	pool *pgxpool.Pool
}

func NewTeamRepository(pool *pgxpool.Pool) *TeamRepository {
	return &TeamRepository{pool: pool}
}

const teamColumns = `
	id, name, sport_id, COALESCE(club_id,''), category,
	COALESCE(logo_url,''), fee_amount, fee_due_day, currency, is_active, created_at`

func scanTeam(row pgx.Row) (*team.Team, error) {
	t := &team.Team{}
	err := row.Scan(
		&t.ID, &t.Name, &t.SportID, &t.ClubID, &t.Category,
		&t.LogoURL, &t.FeeAmount, &t.FeeDueDay, &t.Currency, &t.IsActive, &t.CreatedAt,
	)
	return t, err
}

func (r *TeamRepository) FindByID(ctx context.Context, id string) (*team.Team, error) {
	q := `SELECT` + teamColumns + ` FROM teams WHERE id = $1`
	t, err := scanTeam(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, team.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("team.FindByID: %w", err)
	}
	return t, nil
}

func (r *TeamRepository) Create(ctx context.Context, t *team.Team) error {
	const q = `
		INSERT INTO teams (name, sport_id, club_id, category, logo_url, fee_amount, fee_due_day, currency, is_active)
		VALUES ($1, $2, NULLIF($3,''), $4, NULLIF($5,''), $6, $7, $8, $9)
		RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, q,
		t.Name, t.SportID, t.ClubID, t.Category, t.LogoURL,
		t.FeeAmount, t.FeeDueDay, t.Currency, t.IsActive,
	).Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return fmt.Errorf("team.Create: %w", err)
	}
	return nil
}

func (r *TeamRepository) List(ctx context.Context) ([]*team.Team, error) {
	q := `SELECT` + teamColumns + ` FROM teams WHERE is_active = true ORDER BY name`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("team.List: %w", err)
	}
	defer rows.Close()

	var teams []*team.Team
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, fmt.Errorf("team.List scan: %w", err)
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

func (r *TeamRepository) UpdateFeeConfig(ctx context.Context, id string, cfg team.FeeConfig) error {
	const q = `UPDATE teams SET fee_amount = $1, fee_due_day = $2 WHERE id = $3`
	_, err := r.pool.Exec(ctx, q, cfg.FeeAmount, cfg.FeeDueDay, id)
	if err != nil {
		return fmt.Errorf("team.UpdateFeeConfig: %w", err)
	}
	return nil
}
