package expense

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("expense not found")
	// ErrInvalidSource lo devuelve el handler cuando llega media referencia:
	// tipo sin id o id sin tipo. La base lo rechaza igual, pero acá el error
	// se puede explicar.
	ErrInvalidSource = errors.New("expense source needs both type and id")
)

type SourceType string

const (
	SourceMatchCost SourceType = "match_cost"
)

// Source es de qué salió el gasto. Mismo par (tipo, id) que charges y
// team_funds, para que el balance de un partido cruce las tres por el mismo
// lado.
type Source struct {
	Type SourceType `json:"type"`
	ID   string     `json:"id"`
}

// Expense es plata que salió del equipo.
//
// `Source` es opcional: un gasto puede pertenecer a un partido (el árbitro de
// ese amistoso) o al equipo a secas (pelotas, botiquín). Los que tienen origen
// entran en el balance de ese partido; el resto solo en el total del mes.
type Expense struct {
	ID          string    `json:"id"`
	TeamID      string    `json:"team_id"`
	RecordedBy  string    `json:"recorded_by"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Source      *Source   `json:"source,omitempty"`
	ExpenseDate time.Time `json:"expense_date"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateInput son los datos con los que se anota un gasto. `Source` en nil es un
// gasto del equipo sin partido.
type CreateInput struct {
	TeamID      string
	RecordedBy  string
	Amount      int64
	Currency    string
	Category    string
	Description string
	Source      *Source
	ExpenseDate time.Time
}

type Repository interface {
	Create(ctx context.Context, input CreateInput) (*Expense, error)
	// ListByTeamAndPeriod devuelve los gastos de un mes calendario, por fecha
	// de gasto y no de carga.
	ListByTeamAndPeriod(ctx context.Context, teamID string, year, month int) ([]*Expense, error)
	// ListBySource devuelve los gastos que cuelgan de un origen: los de un
	// partido, para su balance.
	ListBySource(ctx context.Context, source Source) ([]*Expense, error)
	GetByID(ctx context.Context, id string) (*Expense, error)
	Delete(ctx context.Context, id string) error
}
