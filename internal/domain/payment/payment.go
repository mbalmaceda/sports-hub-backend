package payment

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("payment not found")

type Payment struct {
	ID                string
	FeeObligationID   string
	Amount            int64
	Currency          string
	PaidAt            time.Time
	Method            string
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Payment, error)
	FindByFeeObligation(ctx context.Context, feeObligationID string) ([]*Payment, error)
	Create(ctx context.Context, p *Payment) error
}
