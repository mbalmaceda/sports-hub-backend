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

func scanFunds(row pgx.Row) (*funds.Entry, error) {
	e := &funds.Entry{}
	err := row.Scan(
		&e.TeamID, &e.Source.Type, &e.Source.ID,
		&e.Amount, &e.Currency, &e.UpdatedAt, &e.SourceName,
	)
	if err != nil {
		return nil, err
	}
	return e, nil
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

// ListByTeam trae los fondos del equipo con el nombre de su origen resuelto.
//
// El LEFT JOIN es lo que evita el N+1: antes el handler pedía la competencia de
// cada entrada por separado, así que un equipo con treinta partidos hacía
// treinta y una consultas contra una base que está en otra región.
//
// LEFT y no INNER porque un fondo sobrevive a su competencia: si el partido se
// borró, la plata que dejó sigue siendo del equipo y tiene que seguir sumando
// al total, aunque ya no se pueda decir de dónde salió.
func (r *FundsRepository) ListByTeam(ctx context.Context, teamID string) ([]*funds.Entry, error) {
	const q = `
		SELECT f.team_id, f.source_type, f.source_id, f.amount, f.currency, f.updated_at,
		       COALESCE(c.name, '')
		FROM team_funds f
		LEFT JOIN competitions c
		       ON f.source_type = 'match_cost' AND c.id = f.source_id
		WHERE f.team_id = $1
		ORDER BY f.updated_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("funds.ListByTeam: %w", err)
	}
	defer rows.Close()

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*funds.Entry, error) {
		return scanFunds(row)
	})
}
