package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/payment"
)

type PaymentRepository struct {
	pool *pgxpool.Pool
}

func NewPaymentRepository(pool *pgxpool.Pool) *PaymentRepository {
	return &PaymentRepository{pool: pool}
}

const paymentColumns = `
	id, team_id, COALESCE(obligation_id::text,''), payer_id, recorded_by,
	amount, currency, method, COALESCE(notes,''), COALESCE(receipt_url,''),
	is_reversed, created_at`

func scanPayment(row pgx.Row) (*payment.Payment, error) {
	p := &payment.Payment{}
	err := row.Scan(
		&p.ID, &p.TeamID, &p.ObligationID, &p.PayerID, &p.RecordedBy,
		&p.Amount, &p.Currency, &p.Method, &p.Notes, &p.ReceiptURL,
		&p.IsReversed, &p.CreatedAt,
	)
	return p, err
}

func (r *PaymentRepository) FindByID(ctx context.Context, id string) (*payment.Payment, error) {
	q := `SELECT` + paymentColumns + ` FROM payments WHERE id = $1`
	p, err := scanPayment(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, payment.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("payment.FindByID: %w", err)
	}
	return p, nil
}

func (r *PaymentRepository) ListByTeam(ctx context.Context, teamID string) ([]*payment.Payment, error) {
	q := `SELECT` + paymentColumns + ` FROM payments WHERE team_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("payment.ListByTeam: %w", err)
	}
	defer rows.Close()

	var payments []*payment.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, fmt.Errorf("payment.ListByTeam scan: %w", err)
		}
		payments = append(payments, p)
	}
	return payments, rows.Err()
}

func (r *PaymentRepository) FindByObligationID(ctx context.Context, obligationID string) (*payment.Payment, error) {
	q := `SELECT` + paymentColumns + ` FROM payments WHERE obligation_id = $1 AND is_reversed = false LIMIT 1`
	p, err := scanPayment(r.pool.QueryRow(ctx, q, obligationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, payment.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("payment.FindByObligationID: %w", err)
	}
	return p, nil
}

func (r *PaymentRepository) Create(ctx context.Context, p *payment.Payment) error {
	const q = `
		INSERT INTO payments
			(team_id, obligation_id, payer_id, recorded_by, amount, currency, method, notes, receipt_url)
		VALUES ($1, NULLIF($2,'')::uuid, $3, $4, $5, $6, $7, NULLIF($8,''), NULLIF($9,''))
		RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, q,
		p.TeamID, p.ObligationID, p.PayerID, p.RecordedBy,
		p.Amount, p.Currency, p.Method, p.Notes, p.ReceiptURL,
	).Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		return fmt.Errorf("payment.Create: %w", err)
	}
	return nil
}

func (r *PaymentRepository) Reverse(ctx context.Context, id string) error {
	const q = `UPDATE payments SET is_reversed = true WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("payment.Reverse: %w", err)
	}
	return nil
}
