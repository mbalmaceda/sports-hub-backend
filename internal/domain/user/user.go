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
	Phone        string    `json:"phone,omitempty"`
	AvatarURL    string    `json:"avatar_url,omitempty"`
	PushToken    string    `json:"-"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ProfileUpdate struct {
	Name      string
	Phone     string
	AvatarURL string
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	Create(ctx context.Context, u *User) error
	UpdateProfile(ctx context.Context, userID string, update ProfileUpdate) error
	UpdatePushToken(ctx context.Context, userID, token string) error
}
