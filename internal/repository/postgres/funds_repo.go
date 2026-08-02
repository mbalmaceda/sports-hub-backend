package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/funds"
)

type FundsRepository struct {
	pool *pgxpool.Pool
}

func NewFundsRepository(pool *pgxpool.Pool) *FundsRepository {
	return &FundsRepository{pool: pool}
}

const fundsColumns = `team_id, source_type, source_id, amount, currency, updated_at`

func scanFunds(row pgx.Row) (*funds.Entry, error) {
	e := &funds.Entry{}
	err := row.Scan(&e.TeamID, &e.Source.Type, &e.Source.ID, &e.Amount, &e.Currency, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func collectFunds(rows pgx.Rows) ([]*funds.Entry, error) {
	var result []*funds.Entry
	for rows.Next() {
		e, err := scanFunds(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// Set actualiza el fondo de ese origen, o lo borra si el reparto no deja plata.
// La condición del monto cero evita que un partido sin excedente quede figurando
// en la lista de fondos del equipo.
func (r *FundsRepository) Set(
	ctx context.Context, teamID string, source funds.Source, amount int64, currency string,
) error {
	if amount == 0 {
		const del = `DELETE FROM team_funds WHERE team_id = $1 AND source_type = $2 AND source_id = $3`
		if _, err := r.pool.Exec(ctx, del, teamID, source.Type, source.ID); err != nil {
			return fmt.Errorf("funds.Set (delete): %w", err)
		}
		return nil
	}

	const upsert = `
		INSERT INTO team_funds (team_id, source_type, source_id, amount, currency, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (team_id, source_type, source_id)
		DO UPDATE SET amount = EXCLUDED.amount, currency = EXCLUDED.currency, updated_at = NOW()`
	if _, err := r.pool.Exec(ctx, upsert, teamID, source.Type, source.ID, amount, currency); err != nil {
		return fmt.Errorf("funds.Set: %w", err)
	}
	return nil
}

func (r *FundsRepository) ListByTeam(ctx context.Context, teamID string) ([]*funds.Entry, error) {
	q := `SELECT` + fundsColumns + ` FROM team_funds WHERE team_id = $1 ORDER BY updated_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("funds.ListByTeam: %w", err)
	}
	defer rows.Close()
	return collectFunds(rows)
}
