package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
)

type ChargeRepository struct {
	pool *pgxpool.Pool
}

func NewChargeRepository(pool *pgxpool.Pool) *ChargeRepository {
	return &ChargeRepository{pool: pool}
}

const chargeColumns = `
	id, team_id, membership_id, source_type, source_id, amount, currency,
	status, due_date, receipt_url, submitted_at, confirmed_at, confirmed_by, created_at`

func scanCharge(row pgx.Row) (*charge.Charge, error) {
	ch := &charge.Charge{}
	var receiptURL *string
	err := row.Scan(
		&ch.ID, &ch.TeamID, &ch.MembershipID, &ch.Source.Type, &ch.Source.ID,
		&ch.Amount, &ch.Currency, &ch.Status, &ch.DueDate, &receiptURL,
		&ch.SubmittedAt, &ch.ConfirmedAt, &ch.ConfirmedBy, &ch.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if receiptURL != nil {
		ch.ReceiptURL = *receiptURL
	}
	return ch, nil
}

func collectCharges(rows pgx.Rows) ([]*charge.Charge, error) {
	return collect(rows, scanCharge)
}

func (r *ChargeRepository) FindByID(ctx context.Context, id string) (*charge.Charge, error) {
	q := `SELECT` + chargeColumns + ` FROM charges WHERE id = $1`
	ch, err := scanCharge(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, charge.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("charge.FindByID: %w", err)
	}
	return ch, nil
}

func (r *ChargeRepository) ListBySource(ctx context.Context, source charge.Source) ([]*charge.Charge, error) {
	q := `SELECT` + chargeColumns + `
		FROM charges WHERE source_type = $1 AND source_id = $2
		ORDER BY created_at`
	rows, err := r.pool.Query(ctx, q, source.Type, source.ID)
	if err != nil {
		return nil, fmt.Errorf("charge.ListBySource: %w", err)
	}
	defer rows.Close()
	return collectCharges(rows)
}

// ListByTeamAndPeriod filtra por el mes en que el cargo movió plata: la fecha
// de cobro si ya se pagó, la de emisión si no. Rango medio abierto para que el
// índice de `charges (team_id, status)` siga sirviendo de filtro previo.
func (r *ChargeRepository) ListByTeamAndPeriod(
	ctx context.Context, teamID string, year, month int,
) ([]*charge.Charge, error) {
	from := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)

	q := `SELECT` + chargeColumns + `
		FROM charges
		WHERE team_id = $1
		  AND COALESCE(confirmed_at, created_at) >= $2
		  AND COALESCE(confirmed_at, created_at) < $3
		ORDER BY COALESCE(confirmed_at, created_at) DESC`

	rows, err := r.pool.Query(ctx, q, teamID, from, to)
	if err != nil {
		return nil, fmt.Errorf("charge.ListByTeamAndPeriod: %w", err)
	}
	defer rows.Close()
	return collectCharges(rows)
}

func (r *ChargeRepository) ListByMembership(ctx context.Context, membershipID string) ([]*charge.Charge, error) {
	q := `SELECT` + chargeColumns + `
		FROM charges WHERE membership_id = $1
		ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, membershipID)
	if err != nil {
		return nil, fmt.Errorf("charge.ListByMembership: %w", err)
	}
	defer rows.Close()
	return collectCharges(rows)
}

// CreateForSource rehace el reparto conservando lo ya cobrado.
//
// Borra únicamente los cargos pendientes de ese origen y luego inserta los
// nuevos con ON CONFLICT DO NOTHING, de modo que los que quedaron —pagados o
// con comprobante enviado— sobreviven intactos. Si el manager rehace el reparto
// porque cambió la nómina, no puede borrar plata que alguien ya transfirió.
func (r *ChargeRepository) CreateForSource(ctx context.Context, in charge.CreateInput) ([]*charge.Charge, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("charge.CreateForSource: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const clearPending = `
		DELETE FROM charges
		WHERE source_type = $1 AND source_id = $2 AND status = 'pending'`
	if _, err := tx.Exec(ctx, clearPending, in.Source.Type, in.Source.ID); err != nil {
		return nil, fmt.Errorf("charge.CreateForSource: clear: %w", err)
	}

	const insert = `
		INSERT INTO charges
			(team_id, membership_id, source_type, source_id, amount, currency, status, due_date)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)
		ON CONFLICT (source_type, source_id, membership_id) DO NOTHING`
	for _, line := range in.Lines {
		_, err := tx.Exec(ctx, insert,
			in.TeamID, line.MembershipID, in.Source.Type, in.Source.ID,
			line.Amount, in.Currency, in.DueDate,
		)
		if err != nil {
			return nil, fmt.Errorf("charge.CreateForSource: insert: %w", err)
		}
	}

	q := `SELECT` + chargeColumns + `
		FROM charges WHERE source_type = $1 AND source_id = $2
		ORDER BY created_at`
	rows, err := tx.Query(ctx, q, in.Source.Type, in.Source.ID)
	if err != nil {
		return nil, fmt.Errorf("charge.CreateForSource: select: %w", err)
	}
	charges, err := collectCharges(rows)
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("charge.CreateForSource: scan: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("charge.CreateForSource: commit: %w", err)
	}
	return charges, nil
}

// EnsureForMembership crea el cargo de una membresía si todavía no existe.
//
// El ON CONFLICT DO NOTHING más el SELECT de vuelta es lo que lo hace
// idempotente sin pisar nada: dos toques seguidos de "¡Voy!" no duplican el
// cargo, y si el manager ya lo había repartido con otro monto, gana el que ya
// estaba —el suyo es el número acordado, no este.
func (r *ChargeRepository) EnsureForMembership(
	ctx context.Context, in charge.EnsureInput,
) (*charge.Charge, error) {
	const insert = `
		INSERT INTO charges
			(team_id, membership_id, source_type, source_id, amount, currency, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending')
		ON CONFLICT (source_type, source_id, membership_id) DO NOTHING`
	if _, err := r.pool.Exec(ctx, insert,
		in.TeamID, in.MembershipID, in.Source.Type, in.Source.ID, in.Amount, in.Currency,
	); err != nil {
		return nil, fmt.Errorf("charge.EnsureForMembership: insert: %w", err)
	}

	q := `SELECT` + chargeColumns + `
		FROM charges
		WHERE source_type = $1 AND source_id = $2 AND membership_id = $3`
	ch, err := scanCharge(r.pool.QueryRow(ctx, q, in.Source.Type, in.Source.ID, in.MembershipID))
	if err != nil {
		return nil, fmt.Errorf("charge.EnsureForMembership: select: %w", err)
	}
	return ch, nil
}

// RemovePendingForMembership saca el cargo pendiente de una membresía. El
// `status = 'pending'` del WHERE es el que protege lo ya enviado o pagado.
func (r *ChargeRepository) RemovePendingForMembership(
	ctx context.Context, source charge.Source, membershipID string,
) error {
	const q = `
		DELETE FROM charges
		WHERE source_type = $1 AND source_id = $2 AND membership_id = $3 AND status = 'pending'`
	if _, err := r.pool.Exec(ctx, q, source.Type, source.ID, membershipID); err != nil {
		return fmt.Errorf("charge.RemovePendingForMembership: %w", err)
	}
	return nil
}

// SubmitReceipt da el cargo por pagado en el mismo acto: el que transfiere es
// el que declara, y se le cree. El WHERE exige que esté pendiente y hace la
// operación atómica: dos envíos simultáneos y el segundo no afecta filas.
//
// `confirmed_at` se sella acá porque el cargo queda cerrado, pero `confirmed_by`
// se deja en NULL a propósito: nadie verificó nada. Un `paid` con `confirmed_by`
// vacío es exactamente eso —un pago declarado por el deudor— y así se distingue
// de los que sí revisó un tesorero antes de este cambio.
func (r *ChargeRepository) SubmitReceipt(ctx context.Context, id, receiptURL string, at time.Time) (*charge.Charge, error) {
	q := `UPDATE charges
		SET receipt_url = $1, status = 'paid', submitted_at = $2, confirmed_at = $2
		WHERE id = $3 AND status = 'pending'
		RETURNING` + chargeColumns
	ch, err := scanCharge(r.pool.QueryRow(ctx, q, receiptURL, at, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, charge.ErrNotPayable
	}
	if err != nil {
		return nil, fmt.Errorf("charge.SubmitReceipt: %w", err)
	}
	return ch, nil
}

// Confirm solo funciona sobre un cargo con comprobante enviado: no se puede dar
// por pagado algo que nadie subió.
func (r *ChargeRepository) Confirm(ctx context.Context, id, confirmedBy string, at time.Time) (*charge.Charge, error) {
	q := `UPDATE charges
		SET status = 'paid', confirmed_at = $1, confirmed_by = $2
		WHERE id = $3 AND status = 'submitted'
		RETURNING` + chargeColumns
	ch, err := scanCharge(r.pool.QueryRow(ctx, q, at, confirmedBy, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, charge.ErrNotSubmitted
	}
	if err != nil {
		return nil, fmt.Errorf("charge.Confirm: %w", err)
	}
	return ch, nil
}

// Waive condona el cargo. Ver el contrato en charge.Repository.
//
// El WHERE distingue los dos motivos de no hacer nada: si la fila existe pero
// no está pendiente, es que ya se pagó, y eso no es un 404.
func (r *ChargeRepository) Waive(ctx context.Context, id, waivedBy string, at time.Time) (*charge.Charge, error) {
	q := `UPDATE charges
		SET status = 'waived', confirmed_at = $1, confirmed_by = $2
		WHERE id = $3 AND status = 'pending'
		RETURNING` + chargeColumns
	ch, err := scanCharge(r.pool.QueryRow(ctx, q, at, waivedBy, id))
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if e := r.pool.QueryRow(ctx, `SELECT TRUE FROM charges WHERE id = $1`, id).Scan(&exists); e != nil {
			return nil, charge.ErrNotFound
		}
		return nil, charge.ErrAlreadySettled
	}
	if err != nil {
		return nil, fmt.Errorf("charge.Waive: %w", err)
	}
	return ch, nil
}

func (r *ChargeRepository) RejectReceipt(ctx context.Context, id string) (*charge.Charge, error) {
	q := `UPDATE charges
		SET status = 'pending', receipt_url = NULL, submitted_at = NULL
		WHERE id = $1 AND status = 'submitted'
		RETURNING` + chargeColumns
	ch, err := scanCharge(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, charge.ErrNotSubmitted
	}
	if err != nil {
		return nil, fmt.Errorf("charge.RejectReceipt: %w", err)
	}
	return ch, nil
}
