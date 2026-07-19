package team

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("team not found")

type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SportID   string    `json:"sport_id"`
	ClubID    string    `json:"club_id,omitempty"`
	Category  string    `json:"category"`
	LogoURL   string    `json:"logo_url,omitempty"`
	FeeAmount int64     `json:"fee_amount"`
	FeeDueDay int       `json:"fee_due_day"`
	Currency  string    `json:"currency"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

type FeeConfig struct {
	FeeAmount int64
	FeeDueDay int
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Team, error)
	Create(ctx context.Context, t *Team) error
	List(ctx context.Context) ([]*Team, error)
	UpdateFeeConfig(ctx context.Context, id string, cfg FeeConfig) error
}
