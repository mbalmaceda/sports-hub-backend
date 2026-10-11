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
	if len(obligations) == 0 {
		return 0, nil
	}

	/*
		Un solo INSERT con `unnest` en vez de uno por cuota.

		Antes era una transacción con un INSERT por miembro: un plantel de
		veinte eran veintidós idas y vueltas a la base para una sola acción del
		manager. Una sentencia es atómica igual, así que la transacción también
		sobraba.
	*/
	n := len(obligations)
	teamIDs := make([]string, n)
	membershipIDs := make([]string, n)
	years := make([]int32, n)
	months := make([]int32, n)
	amounts := make([]int64, n)
	currencies := make([]string, n)
	dueDates := make([]string, n)
	statuses := make([]string, n)
	for i, o := range obligations {
		teamIDs[i] = o.TeamID
		membershipIDs[i] = o.MembershipID
		years[i] = int32(o.PeriodYear)
		months[i] = int32(o.PeriodMonth)
		amounts[i] = o.Amount
		currencies[i] = o.Currency
		// Como texto y no como instante: un `timestamptz` pasado a `date` se lee
		// en la zona de la sesión, y medianoche UTC en Chile es el día anterior.
		dueDates[i] = o.DueDate.Format(time.DateOnly)
		statuses[i] = string(o.Status)
	}

	const q = `
		INSERT INTO fee_obligations
			(team_id, membership_id, period_year, period_month, amount, currency, due_date, status)
		SELECT * FROM unnest(
			$1::uuid[], $2::uuid[], $3::int4[]::int2[], $4::int4[]::int2[],
			$5::int8[], $6::text[], $7::date[], $8::text[]
		)
		ON CONFLICT (membership_id, period_year, period_month) DO NOTHING`
	tag, err := r.pool.Exec(ctx, q,
		teamIDs, membershipIDs, years, months, amounts, currencies, dueDates, statuses)
	if err != nil {
		return 0, fmt.Errorf("fee.BulkCreate: %w", err)
	}
	return int(tag.RowsAffected()), nil
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

/*
ListOverdue son las cuotas impagas cuyo vencimiento ya pasó, de todos los
equipos.

Los dos filtros del final no son de más. `fee_amount > 0` deja afuera a los
equipos que no cobran cuota mensual —donde un "no pagó" sería falso: no es que
nadie pagó, es que no hay nada que pagar— y `kind <> 'guest'` a los invitados,
que juegan un partido y no pertenecen al club.

Mira `status = 'pending'` y la fecha, no `status = 'overdue'`: nadie mueve ese
estado solo, así que esperar a que alguien lo marque sería no avisar nunca.
*/
func (r *FeeRepository) ListOverdue(ctx context.Context, on time.Time) ([]*fee.Overdue, error) {
	const q = `
		SELECT o.id, o.team_id, o.membership_id, u.name,
		       o.period_year, o.period_month, o.amount, o.currency
		FROM fee_obligations o
		JOIN memberships m ON m.id = o.membership_id
		JOIN users       u ON u.id = m.user_id
		JOIN teams       t ON t.id = o.team_id
		WHERE o.status = 'pending'
		  AND o.due_date < $1::date
		  AND m.status = 'active'
		  AND m.kind <> 'guest'
		  AND t.fee_amount > 0
		  AND u.deleted_at IS NULL
		ORDER BY o.team_id, o.due_date`
	rows, err := r.pool.Query(ctx, q, on)
	if err != nil {
		return nil, fmt.Errorf("fee.ListOverdue: %w", err)
	}
	defer rows.Close()

	return collect(rows, func(row pgx.Row) (*fee.Overdue, error) {
		o := &fee.Overdue{}
		err := row.Scan(&o.ObligationID, &o.TeamID, &o.MembershipID, &o.FullName,
			&o.PeriodYear, &o.PeriodMonth, &o.Amount, &o.Currency)
		return o, err
	})
}
