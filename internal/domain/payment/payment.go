package payment

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("payment not found")

type Method string

const (
	MethodCash     Method = "cash"
	MethodTransfer Method = "transfer"
	MethodOnline   Method = "online"
	MethodOther    Method = "other"
)

type Payment struct {
	ID           string    `json:"id"`
	TeamID       string    `json:"team_id"`
	ObligationID string    `json:"obligation_id,omitempty"`
	PayerID      string    `json:"payer_id"`
	RecordedBy   string    `json:"recorded_by"`
	Amount       int64     `json:"amount"`
	Currency     string    `json:"currency"`
	Method       Method    `json:"method"`
	Notes        string    `json:"notes,omitempty"`
	ReceiptURL   string    `json:"receipt_url,omitempty"`
	IsReversed   bool      `json:"is_reversed"`
	CreatedAt    time.Time `json:"created_at"`
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Payment, error)
	ListByTeam(ctx context.Context, teamID string) ([]*Payment, error)
	FindByObligationID(ctx context.Context, obligationID string) (*Payment, error)
	Create(ctx context.Context, p *Payment) error
	Reverse(ctx context.Context, id string) error
}
