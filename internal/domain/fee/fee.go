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

type Repository interface {
	FindByID(ctx context.Context, id string) (*Obligation, error)
	ListByTeamAndPeriod(ctx context.Context, teamID string, year, month int) ([]*Obligation, error)
	ListByMembership(ctx context.Context, membershipID string) ([]*Obligation, error)
	Create(ctx context.Context, o *Obligation) error
	BulkCreate(ctx context.Context, obligations []*Obligation) (int, error)
	UpdateStatus(ctx context.Context, id string, status Status, paidAt *time.Time) error
}
