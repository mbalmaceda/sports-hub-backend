package user

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("user not found")

// User no tiene rol: el rol es por membership (ver domain/membership),
// porque un usuario puede tener un rol distinto en cada equipo.
type User struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	PushToken    string    `json:"-"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	Create(ctx context.Context, u *User) error
	UpdatePushToken(ctx context.Context, userID, token string) error
}
