package team

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("team not found")

type Team struct {
	ID        string
	Name      string
	Sport     string
	CreatedAt time.Time
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Team, error)
	Create(ctx context.Context, t *Team) error
	List(ctx context.Context) ([]*Team, error)
}
