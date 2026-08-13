package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/fee"
)

type FeeRepository struct {
	pool *pgxpool.Pool
}

func NewFeeRepository(pool *pgxpool.Pool) *FeeRepository {
	return &FeeRepository{pool: pool}
}

const feeColumns = `
	id, team_id, membership_id, period_year, period_month,
	amount, currency, due_date, status, paid_at, created_at`

func scanObligation(row pgx.Row) (*fee.Obligation, error) {
	o := &fee.Obligation{}
	err := row.Scan(
		&o.ID, &o.TeamID, &o.MembershipID, &o.PeriodYear, &o.PeriodMonth,
		&o.Amount, &o.Currency, &o.DueDate, &o.Status, &o.PaidAt, &o.CreatedAt,
	)
	return o, err
}

func (r *FeeRepository) FindByID(ctx context.Context, id string) (*fee.Obligation, error) {
	q := `SELECT` + feeColumns + ` FROM fee_obligations WHERE id = $1`
	o, err := scanObligation(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fee.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fee.FindByID: %w", err)
	}
	return o, nil
}

func (r *FeeRepository) ListByTeamAndPeriod(ctx context.Context, teamID string, year, month int) ([]*fee.Obligation, error) {
	q := `SELECT` + feeColumns + `
		FROM fee_obligations
		WHERE team_id = $1 AND period_year = $2 AND period_month = $3
		ORDER BY created_at`
	rows, err := r.pool.Query(ctx, q, teamID, year, month)
	if err != nil {
		return nil, fmt.Errorf("fee.ListByTeamAndPeriod: %w", err)
	}
	defer rows.Close()
	return collectObligations(rows)
}

func (r *FeeRepository) ListByMembership(ctx context.Context, membershipID string) ([]*fee.Obligation, error) {
	q := `SELECT` + feeColumns + `
		FROM fee_obligations
		WHERE membership_id = $1
		ORDER BY period_year DESC, period_month DESC`
	rows, err := r.pool.Query(ctx, q, membershipID)
	if err != nil {
		return nil, fmt.Errorf("fee.ListByMembership: %w", err)
	}
	defer rows.Close()
	return collectObligations(rows)
}

func (r *FeeRepository) Create(ctx context.Context, o *fee.Obligation) error {
	const q = `
		INSERT INTO fee_obligations
			(team_id, membership_id, period_year, period_month, amount, currency, due_date, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, q,
		o.TeamID, o.MembershipID, o.PeriodYear, o.PeriodMonth,
		o.Amount, o.Currency, o.DueDate, o.Status,
	).Scan(&o.ID, &o.CreatedAt)
	if err != nil {
		return fmt.Errorf("fee.Create: %w", err)
	}
	return nil
}

// BulkCreate genera cuotas para múltiples memberships de una vez.
// Usa ON CONFLICT DO NOTHING para que sea idempotente.
func (r *FeeRepository) BulkCreate(ctx context.Context, obligations []*fee.Obligation) (int, error) {
	const q = `
		INSERT INTO fee_obligations
			(team_id, membership_id, period_year, period_month, amount, currency, due_date, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (membership_id, period_year, period_month) DO NOTHING`

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("fee.BulkCreate: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	created := 0
	for _, o := range obligations {
		tag, err := tx.Exec(ctx, q,
			o.TeamID, o.MembershipID, o.PeriodYear, o.PeriodMonth,
			o.Amount, o.Currency, o.DueDate, o.Status,
		)
		if err != nil {
			return 0, fmt.Errorf("fee.BulkCreate: insert: %w", err)
		}
		created += int(tag.RowsAffected())
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("fee.BulkCreate: commit: %w", err)
	}
	return created, nil
}

func (r *FeeRepository) UpdateStatus(ctx context.Context, id string, status fee.Status, paidAt *time.Time) error {
	const q = `UPDATE fee_obligations SET status = $1, paid_at = $2 WHERE id = $3`
	_, err := r.pool.Exec(ctx, q, status, paidAt, id)
	if err != nil {
		return fmt.Errorf("fee.UpdateStatus: %w", err)
	}
	return nil
}

func collectObligations(rows pgx.Rows) ([]*fee.Obligation, error) {
	return collect(rows, scanObligation)
}
