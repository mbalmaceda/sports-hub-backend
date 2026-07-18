package membership

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("membership not found")

type Status string

const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
	StatusPending  Status = "pending"
)

type Membership struct {
	ID       string
	UserID   string
	TeamID   string
	Status   Status
	JoinedAt time.Time
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Membership, error)
	FindByUserAndTeam(ctx context.Context, userID, teamID string) (*Membership, error)
	FindByTeam(ctx context.Context, teamID string) ([]*Membership, error)
	FindByUser(ctx context.Context, userID string) ([]*Membership, error)
	Create(ctx context.Context, m *Membership) error
	UpdateStatus(ctx context.Context, id string, status Status) error
}
