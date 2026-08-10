package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/expense"
)

type ExpenseRepository struct {
	pool *pgxpool.Pool
}

func NewExpenseRepository(pool *pgxpool.Pool) *ExpenseRepository {
	return &ExpenseRepository{pool: pool}
}

const expenseColumns = `
	id, team_id, recorded_by, amount, currency, category, description,
	source_type, source_id, expense_date, created_at`

// scanExpense arma el gasto desde la fila. `recorded_by` y el par del origen son
// nullables, así que pasan por punteros antes de aterrizar en la entidad: el
// origen queda en nil cuando el gasto no cuelga de ningún partido.
func scanExpense(row pgx.Row) (*expense.Expense, error) {
	e := &expense.Expense{}
	var recordedBy, sourceType, sourceID *string

	err := row.Scan(
		&e.ID, &e.TeamID, &recordedBy, &e.Amount, &e.Currency, &e.Category,
		&e.Description, &sourceType, &sourceID, &e.ExpenseDate, &e.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if recordedBy != nil {
		e.RecordedBy = *recordedBy
	}
	if sourceType != nil && sourceID != nil {
		e.Source = &expense.Source{Type: expense.SourceType(*sourceType), ID: *sourceID}
	}
	return e, nil
}

func collectExpenses(rows pgx.Rows) ([]*expense.Expense, error) {
	var result []*expense.Expense
	for rows.Next() {
		e, err := scanExpense(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

func (r *ExpenseRepository) Create(ctx context.Context, input expense.CreateInput) (*expense.Expense, error) {
	var sourceType, sourceID *string
	if input.Source != nil {
		t := string(input.Source.Type)
		sourceType, sourceID = &t, &input.Source.ID
	}

	var recordedBy *string
	if input.RecordedBy != "" {
		recordedBy = &input.RecordedBy
	}

	q := `
		INSERT INTO expenses (
			team_id, recorded_by, amount, currency, category, description,
			source_type, source_id, expense_date
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING` + expenseColumns

	e, err := scanExpense(r.pool.QueryRow(ctx, q,
		input.TeamID, recordedBy, input.Amount, input.Currency, input.Category,
		input.Description, sourceType, sourceID, input.ExpenseDate,
	))
	if err != nil {
		return nil, fmt.Errorf("expense.Create: %w", err)
	}
	return e, nil
}

// ListByTeamAndPeriod filtra por mes calendario con un rango medio abierto sobre
// `expense_date`. Comparar con EXTRACT dejaría el índice afuera y obligaría a
// leer todos los gastos del equipo para quedarse con los de un mes.
func (r *ExpenseRepository) ListByTeamAndPeriod(
	ctx context.Context, teamID string, year, month int,
) ([]*expense.Expense, error) {
	from := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)

	q := `SELECT` + expenseColumns + `
		FROM expenses
		WHERE team_id = $1 AND expense_date >= $2 AND expense_date < $3
		ORDER BY expense_date DESC, created_at DESC`

	rows, err := r.pool.Query(ctx, q, teamID, from, to)
	if err != nil {
		return nil, fmt.Errorf("expense.ListByTeamAndPeriod: %w", err)
	}
	defer rows.Close()
	return collectExpenses(rows)
}

func (r *ExpenseRepository) ListBySource(ctx context.Context, source expense.Source) ([]*expense.Expense, error) {
	q := `SELECT` + expenseColumns + `
		FROM expenses
		WHERE source_type = $1 AND source_id = $2
		ORDER BY expense_date DESC, created_at DESC`

	rows, err := r.pool.Query(ctx, q, source.Type, source.ID)
	if err != nil {
		return nil, fmt.Errorf("expense.ListBySource: %w", err)
	}
	defer rows.Close()
	return collectExpenses(rows)
}

func (r *ExpenseRepository) GetByID(ctx context.Context, id string) (*expense.Expense, error) {
	q := `SELECT` + expenseColumns + ` FROM expenses WHERE id = $1`
	e, err := scanExpense(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, expense.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("expense.GetByID: %w", err)
	}
	return e, nil
}

func (r *ExpenseRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM expenses WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("expense.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return expense.ErrNotFound
	}
	return nil
}
