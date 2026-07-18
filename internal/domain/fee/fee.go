package fee

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("fee obligation not found")

type Status string

const (
	StatusPending Status = "pending"
	StatusPaid    Status = "paid"
	StatusOverdue Status = "overdue"
)

// Obligation representa una cuota que un miembro debe pagar.
// Amount está en centavos para evitar errores de punto flotante.
type Obligation struct {
	ID           string
	MembershipID string
	Amount       int64
	Currency     string
	DueDate      time.Time
	Status       Status
	Description  string
	CreatedAt    time.Time
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Obligation, error)
	FindByMembership(ctx context.Context, membershipID string) ([]*Obligation, error)
	FindOverdue(ctx context.Context) ([]*Obligation, error)
	Create(ctx context.Context, o *Obligation) error
	UpdateStatus(ctx context.Context, id string, status Status) error
}
