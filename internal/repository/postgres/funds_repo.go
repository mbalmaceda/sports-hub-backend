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

/*
ListByTeam deriva los fondos del equipo de sus cobros de cancha.

Por partido: lo cobrado a los propios confirmados menos lo que al equipo le toca
poner del lugar. Las tres reglas que lo acotan:

  - Cuenta el cobro emitido, pagado o no. Es el mismo criterio que tenía el
    reparto —el fondo es lo que el partido deja, no lo que ya entró—; lo que
    falta cobrar lo dice el Resumen por su lado.
  - Lo que le toca poner es la mitad del lugar (`charge.TeamShare`, el mismo
    redondeo hacia arriba), salvo en un partido interno, donde los dos lados son
    nuestros y el lugar entero es suyo. El organizador paga el entero pero recibe
    la otra mitad del rival, así que para él también es la mitad.
  - Solo partidos que ya empezaron y no se cancelaron. Antes del pitazo cada
    confirmación movería el número, y en un partido que recién junta gente el
    fondo saldría negativo aunque nadie haya perdido nada.
*/
func (r *FundsRepository) ListByTeam(ctx context.Context, teamID string) ([]*funds.Entry, error) {
	const q = `
		SELECT ch.team_id, 'match_cost', c.id,
		       (SUM(ch.amount) - CASE WHEN c.is_internal
		                              THEN c.venue_cost_amount
		                              ELSE (c.venue_cost_amount + 1) / 2 END)::bigint AS amount,
		       COALESCE(c.venue_cost_currency, 'CLP'), c.start_at, c.name
		FROM charges ch
		JOIN competitions c ON c.id = ch.source_id
		WHERE ch.team_id = $1
		  AND ch.source_type = 'match_cost'
		  AND ch.status <> 'cancelled'
		  AND c.status <> 'cancelled'
		  AND c.venue_cost_amount > 0
		  AND c.start_at <= NOW()
		GROUP BY ch.team_id, c.id
		HAVING SUM(ch.amount) <> CASE WHEN c.is_internal
		                              THEN c.venue_cost_amount
		                              ELSE (c.venue_cost_amount + 1) / 2 END
		ORDER BY c.start_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("funds.ListByTeam: %w", err)
	}
	defer rows.Close()

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*funds.Entry, error) {
		return scanFunds(row)
	})
}
