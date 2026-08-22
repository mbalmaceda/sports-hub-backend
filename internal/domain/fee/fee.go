package fee

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("fee obligation not found")

type Status string

const (
	StatusPending  Status = "pending"
	StatusPaid     Status = "paid"
	StatusOverdue  Status = "overdue"
	StatusExempted Status = "exempted"
)

type Obligation struct {
	ID           string     `json:"id"`
	TeamID       string     `json:"team_id"`
	MembershipID string     `json:"membership_id"`
	PeriodYear   int        `json:"period_year"`
	PeriodMonth  int        `json:"period_month"`
	Amount       int64      `json:"amount"`
	Currency     string     `json:"currency"`
	DueDate      time.Time  `json:"due_date"`
	Status       Status     `json:"status"`
	PaidAt       *time.Time `json:"paid_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

/*
Overdue es una cuota que se pasó de fecha y sigue sin pagarse.

Trae el nombre y la membresía porque el aviso los necesita a los dos: el nombre
para el texto —"Kai no pagó junio" contra "alguien no pagó"— y la membresía
porque es a donde lleva, la ficha del jugador con su historial de cuotas.
*/
type Overdue struct {
	ObligationID string
	TeamID       string
	MembershipID string
	FullName     string
	PeriodYear   int
	PeriodMonth  int
	Amount       int64
	Currency     string
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Obligation, error)
	ListByTeamAndPeriod(ctx context.Context, teamID string, year, month int) ([]*Obligation, error)
	ListByMembership(ctx context.Context, membershipID string) ([]*Obligation, error)
	Create(ctx context.Context, o *Obligation) error
	BulkCreate(ctx context.Context, obligations []*Obligation) (int, error)
	UpdateStatus(ctx context.Context, id string, status Status, paidAt *time.Time) error
	/*
		ListOverdue son las cuotas impagas cuyo vencimiento ya pasó, de todos los
		equipos. Es la consulta del trabajo periódico, así que no recibe equipo.

		Deja afuera a los equipos sin cuota mensual (`fee_amount = 0`): ahí no es
		que nadie pagó, es que esa economía no existe para ese equipo —la misma
		regla que esconde la pestaña de Cuotas en la app—. Y a los invitados, que
		no tienen cuota mensual por definición.
	*/
	ListOverdue(ctx context.Context, on time.Time) ([]*Overdue, error)
}
