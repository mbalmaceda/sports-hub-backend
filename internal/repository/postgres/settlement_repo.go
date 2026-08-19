package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/settlement"
)

type SettlementRepository struct {
	pool *pgxpool.Pool
}

func NewSettlementRepository(pool *pgxpool.Pool) *SettlementRepository {
	return &SettlementRepository{pool: pool}
}

// Ojo con el espacio inicial: se concatena a `SELECT`/`RETURNING` crudos, y sin
// el espacio saldría `SELECTid`.
const settlementColumns = ` id, source_type, source_id, from_team_id, to_team_id,
	amount, currency, status, paid_at, paid_by, created_at`

func scanSettlement(row pgx.Row) (*settlement.Settlement, error) {
	s := &settlement.Settlement{}
	var paidBy *string
	err := row.Scan(
		&s.ID, &s.Source.Type, &s.Source.ID, &s.FromTeamID, &s.ToTeamID,
		&s.Amount, &s.Currency, &s.Status, &s.PaidAt, &paidBy, &s.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if paidBy != nil {
		s.PaidBy = *paidBy
	}
	return s, nil
}

func (r *SettlementRepository) FindByID(ctx context.Context, id string) (*settlement.Settlement, error) {
	q := `SELECT` + settlementColumns + ` FROM team_settlements WHERE id = $1`
	s, err := scanSettlement(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, settlement.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("settlement.FindByID: %w", err)
	}
	return s, nil
}

func (r *SettlementRepository) FindBySource(
	ctx context.Context, source settlement.Source,
) (*settlement.Settlement, error) {
	q := `SELECT` + settlementColumns + `
		FROM team_settlements WHERE source_type = $1 AND source_id = $2`
	s, err := scanSettlement(r.pool.QueryRow(ctx, q, source.Type, source.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, settlement.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("settlement.FindBySource: %w", err)
	}
	return s, nil
}

// ListByTeam trae las dos direcciones: lo que el equipo debe y lo que le deben.
// Los pendientes primero, que es lo que exige acción.
func (r *SettlementRepository) ListByTeam(
	ctx context.Context, teamID string,
) ([]*settlement.Settlement, error) {
	q := `SELECT` + settlementColumns + `
		FROM team_settlements
		WHERE from_team_id = $1 OR to_team_id = $1
		ORDER BY (status = 'pending') DESC, created_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("settlement.ListByTeam: %w", err)
	}
	defer rows.Close()
	return collect(rows, scanSettlement)
}

// Create anota la deuda, o devuelve la que ya estaba.
//
// El `DO UPDATE` sobre una columna que no cambia es el truco para que el
// RETURNING traiga la fila existente en vez de nada: con `DO NOTHING`, un
// segundo intento no devuelve filas y habría que salir a buscarla. Aceptar el
// mismo amistoso dos veces —un reintento de red, un doble toque— no puede
// generar dos deudas ni un error.
func (r *SettlementRepository) Create(
	ctx context.Context, s *settlement.Settlement,
) (*settlement.Settlement, error) {
	q := `
		INSERT INTO team_settlements
			(source_type, source_id, from_team_id, to_team_id, amount, currency)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source_type, source_id)
		DO UPDATE SET source_id = EXCLUDED.source_id
		RETURNING` + settlementColumns
	saved, err := scanSettlement(r.pool.QueryRow(ctx, q,
		s.Source.Type, s.Source.ID, s.FromTeamID, s.ToTeamID, s.Amount, s.Currency,
	))
	if err != nil {
		return nil, fmt.Errorf("settlement.Create: %w", err)
	}
	return saved, nil
}

// MarkPaid cierra la deuda. El `status = 'pending'` del WHERE es lo que hace
// que declarar dos veces no pise el autor ni la fecha del primer pago: la
// segunda no encuentra fila y el handler la traduce a ErrAlreadyPaid.
func (r *SettlementRepository) MarkPaid(
	ctx context.Context, id, paidBy string, at time.Time,
) (*settlement.Settlement, error) {
	q := `
		UPDATE team_settlements
		SET status = 'paid', paid_at = $2, paid_by = $3
		WHERE id = $1 AND status = 'pending'
		RETURNING` + settlementColumns
	s, err := scanSettlement(r.pool.QueryRow(ctx, q, id, at, nullIfEmpty(paidBy)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, settlement.ErrAlreadyPaid
	}
	if err != nil {
		return nil, fmt.Errorf("settlement.MarkPaid: %w", err)
	}
	return s, nil
}
