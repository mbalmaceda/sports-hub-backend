package auth

import (
	"context"
	"errors"
	"time"
)

var ErrTokenNotFound = errors.New("refresh token not found")

type RefreshToken struct {
	ID        string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type RefreshTokenRepository interface {
	Create(ctx context.Context, t *RefreshToken) error
	FindByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	Delete(ctx context.Context, id string) error
	DeleteByHash(ctx context.Context, tokenHash string) error
}
