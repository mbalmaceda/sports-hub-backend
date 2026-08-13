package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func isNameTakenConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "teams_name_lower_key"
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
	if isNameTakenConflict(err) {
		return team.ErrNameTaken
	}
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

	teams, err := collect(rows, scanTeam)
	if err != nil {
		return nil, fmt.Errorf("team.List scan: %w", err)
	}
	return teams, nil
}

func (r *TeamRepository) UpdateFeeConfig(ctx context.Context, id string, cfg team.FeeConfig) error {
	const q = `UPDATE teams SET fee_amount = $1, fee_due_day = $2 WHERE id = $3`
	_, err := r.pool.Exec(ctx, q, cfg.FeeAmount, cfg.FeeDueDay, id)
	if err != nil {
		return fmt.Errorf("team.UpdateFeeConfig: %w", err)
	}
	return nil
}

// SearchByName busca equipos por nombre parcial, para que alguien sin equipo
// encuentre el suyo y pida entrar.
//
// A diferencia de la búsqueda de personas, acá sí se permite coincidencia
// parcial: un equipo es una entidad pública, no un dato personal.
func (r *TeamRepository) SearchByName(ctx context.Context, query string) ([]*team.Team, error) {
	q := `SELECT` + teamColumns + `
		FROM teams
		WHERE is_active AND name ILIKE '%' || $1 || '%'
		ORDER BY name
		LIMIT 20`
	rows, err := r.pool.Query(ctx, q, query)
	if err != nil {
		return nil, fmt.Errorf("team.SearchByName: %w", err)
	}
	defer rows.Close()

	teams, err := collect(rows, scanTeam)
	if err != nil {
		return nil, fmt.Errorf("team.SearchByName scan: %w", err)
	}
	return teams, nil
}

const bankAccountColumns = `
	team_id, bank_name, account_type, account_number, holder_name, holder_tax_id, updated_at`

func (r *TeamRepository) GetBankAccount(ctx context.Context, teamID string) (*team.BankAccount, error) {
	q := `SELECT` + bankAccountColumns + ` FROM team_bank_accounts WHERE team_id = $1`
	acc := &team.BankAccount{}
	err := r.pool.QueryRow(ctx, q, teamID).Scan(
		&acc.TeamID, &acc.BankName, &acc.AccountType, &acc.AccountNumber,
		&acc.HolderName, &acc.HolderTaxID, &acc.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, team.ErrBankAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("team.GetBankAccount: %w", err)
	}
	return acc, nil
}

func (r *TeamRepository) SaveBankAccount(ctx context.Context, acc *team.BankAccount) error {
	const q = `
		INSERT INTO team_bank_accounts
			(team_id, bank_name, account_type, account_number, holder_name, holder_tax_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (team_id) DO UPDATE SET
			bank_name = EXCLUDED.bank_name,
			account_type = EXCLUDED.account_type,
			account_number = EXCLUDED.account_number,
			holder_name = EXCLUDED.holder_name,
			holder_tax_id = EXCLUDED.holder_tax_id,
			updated_at = NOW()
		RETURNING updated_at`
	err := r.pool.QueryRow(ctx, q,
		acc.TeamID, acc.BankName, acc.AccountType, acc.AccountNumber,
		acc.HolderName, acc.HolderTaxID,
	).Scan(&acc.UpdatedAt)
	if err != nil {
		return fmt.Errorf("team.SaveBankAccount: %w", err)
	}
	return nil
}
