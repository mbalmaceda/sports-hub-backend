package user

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("user not found")

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleCoach  Role = "coach"
	RolePlayer Role = "player"
)

type User struct {
	ID        string
	Name      string
	Email     string
	Role      Role
	PushToken string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	Create(ctx context.Context, u *User) error
	UpdatePushToken(ctx context.Context, userID, token string) error
}
